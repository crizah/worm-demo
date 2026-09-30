// Package traffic generates ongoing simulated INSERT/UPDATE/DELETE
// activity against the demo Postgres database, rate-limited by the
// traffic dial. Everything here runs through the restricted demo_writer
// role (see sql/restricted_role.sql) - same permission boundary
// user-submitted SQL will use later, on purpose.
//
// Runs its own continuous worker pool, not a cron.Scheduler task - a
// fixed-interval batch job is the wrong shape for "N requests/sec,
// smoothly, and precisely tunable by a live dial". See Start.
package traffic

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"math/rand"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	workerCount = 6 // concurrent writers sharing the rate limiter - enough to overlap round-trip latency at the high end without being backfill-sized

	idCacheSize         = 200
	idCacheRefreshEvery = 5 * time.Second
)

// dialReader is the one method needed from dial.Service - kept as a small
// interface so this package doesn't depend on decay/clamping/HTTP.
type dialReader interface {
	Get() int
}

// Generator runs a pool of writer goroutines continuously, rate-limited by
// the current traffic dial, doing single INSERT/UPDATE/DELETE operations -
// not periodic batches - so throughput is smooth and hits a precise
// target rate instead of being a side effect of a batch size.
type Generator struct {
	db      *sql.DB
	dial    dialReader
	limiter *rate.Limiter

	projectIDs *idCache
	userIDs    *idCache
	taskIDs    *idCache
}

func NewGenerator(db *sql.DB, dial dialReader) *Generator {
	return &Generator{
		db:         db,
		dial:       dial,
		limiter:    rate.NewLimiter(rate.Limit(dial.Get()), workerCount),
		projectIDs: newIDCache(),
		userIDs:    newIDCache(),
		taskIDs:    newIDCache(),
	}
}

// Start launches the worker pool, the rate<->dial sync loop, and the id
// cache refresh loop. Returns immediately. Everything stops when ctx is
// cancelled. Safe to call even before the schema exists yet (e.g. right
// after a reset, before migrate-schema has run) - refreshes just log and
// retry on the next tick, and writes no-op until the caches have
// something in them (see writeOne).
func (g *Generator) Start(ctx context.Context) {
	go g.refreshLoop(ctx)
	go g.syncRate(ctx)
	for i := 0; i < workerCount; i++ {
		go g.worker(ctx)
	}
}

// syncRate keeps the limiter matched to the dial. The dial's own units are
// already requests/sec (see internal/dial), so this is a direct copy, no
// interpolation needed.
func (g *Generator) syncRate(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			g.limiter.SetLimit(rate.Limit(g.dial.Get()))
		}
	}
}

func (g *Generator) worker(ctx context.Context) {
	for {
		if err := g.limiter.Wait(ctx); err != nil {
			return // ctx cancelled
		}
		if err := g.writeOne(ctx); err != nil {
			log.Printf("[traffic] write failed: %v", err)
		}
	}
}

var taskStatuses = []string{"todo", "in_progress", "done", "archived"}

func (g *Generator) writeOne(ctx context.Context) error {
	switch rand.Intn(4) {
	case 0:
		return g.insertTask(ctx)
	case 1:
		return g.updateRandomTask(ctx)
	case 2:
		return g.insertComment(ctx)
	default:
		return g.deleteOldComment(ctx)
	}
}

func (g *Generator) insertTask(ctx context.Context) error {
	projectID := g.projectIDs.pick()
	if projectID == "" {
		return nil // caches not warm yet
	}
	var assignee any
	if rand.Float64() < 0.8 {
		if uid := g.userIDs.pick(); uid != "" {
			assignee = uid
		}
	}
	status := taskStatuses[rand.Intn(len(taskStatuses))]
	title := fmt.Sprintf("Traffic task %d", time.Now().UnixNano())

	_, err := g.db.ExecContext(ctx,
		`INSERT INTO tasks (project_id, assignee_id, title, status, metadata)
		 VALUES ($1, $2, $3, $4, '{}')
		 ON CONFLICT (assignee_id) WHERE status = 'in_progress' DO NOTHING`,
		projectID, assignee, title, status,
	)
	return err
}

// updateRandomTask can legitimately collide with the partial-unique "one
// in_progress task per assignee" index - that's just skipped, not an error.
func (g *Generator) updateRandomTask(ctx context.Context) error {
	id := g.taskIDs.pick()
	if id == "" {
		return nil
	}
	status := taskStatuses[rand.Intn(len(taskStatuses))]
	_, err := g.db.ExecContext(ctx, `UPDATE tasks SET status = $1 WHERE id = $2`, status, id)
	if err != nil && !isUniqueViolation(err) {
		return err
	}
	return nil
}

func (g *Generator) insertComment(ctx context.Context) error {
	taskID := g.taskIDs.pick()
	userID := g.userIDs.pick()
	if taskID == "" || userID == "" {
		return nil
	}
	body := fmt.Sprintf("Auto comment %d", time.Now().UnixNano())
	_, err := g.db.ExecContext(ctx,
		`INSERT INTO task_comments (task_id, user_id, body) VALUES ($1, $2, $3)`,
		taskID, userID, body,
	)
	return err
}

// deleteOldComment removes the single oldest task_comment - a real WHERE
// clause, same shape user-submitted DELETEs will have to follow later. A
// stale/already-deleted pick just affects 0 rows, harmless.
func (g *Generator) deleteOldComment(ctx context.Context) error {
	_, err := g.db.ExecContext(ctx,
		`DELETE FROM task_comments WHERE id = (
			SELECT id FROM task_comments ORDER BY created_at ASC LIMIT 1
		)`,
	)
	return err
}

// refreshLoop keeps the id caches populated. Runs on its own schedule,
// independent of the write rate - at up to 600 writes/sec, re-querying
// Postgres for "pick one random id" on every single write would itself
// become real load, so this amortizes that to one query per table every
// few seconds instead, and writes pick from memory.
func (g *Generator) refreshLoop(ctx context.Context) {
	refresh := func() {
		for name, c := range map[string]*idCache{"projects": g.projectIDs, "users": g.userIDs, "tasks": g.taskIDs} {
			if err := c.refresh(ctx, g.db, name); err != nil {
				log.Printf("[traffic] refreshing %s id cache: %v", name, err)
			}
		}
	}

	refresh()

	ticker := time.NewTicker(idCacheRefreshEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refresh()
		}
	}
}

// idCache holds a random sample of one table's ids, refreshed
// periodically, served from memory.
type idCache struct {
	mu  sync.RWMutex
	ids []string
}

func newIDCache() *idCache {
	return &idCache{}
}

func (c *idCache) pick() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.ids) == 0 {
		return ""
	}
	return c.ids[rand.Intn(len(c.ids))]
}

func (c *idCache) refresh(ctx context.Context, db *sql.DB, table string) error {
	rows, err := db.QueryContext(ctx, fmt.Sprintf("SELECT id FROM %s ORDER BY random() LIMIT %d", table, idCacheSize))
	if err != nil {
		return err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	c.mu.Lock()
	c.ids = ids
	c.mu.Unlock()
	return nil
}

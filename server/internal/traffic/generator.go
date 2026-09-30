// Package traffic generates ongoing simulated INSERT/UPDATE/DELETE
// activity against the demo Postgres database, scaled by the traffic
// dial. Everything here runs through the restricted demo_writer role
// (see sql/restricted_role.sql) - same permission boundary user-submitted
// SQL will use later, on purpose.
package traffic

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"time"
)

// dialReader is the one method the generator needs from dial.Service -
// kept as a small interface here instead of importing the dial package
// directly, so this package doesn't need to know anything about decay/
// clamping/HTTP.
type dialReader interface {
	Get() int
}

// Generator does one small batch of writes per tick, spread across
// insert/update/delete so the demo actually exercises all three (not just
// inserts) - matches the shape worm's streaming code
// (data-migrator/postgresql.go) has to handle in the real tool.
type Generator struct {
	db   *sql.DB
	dial dialReader
}

func NewGenerator(db *sql.DB, dial dialReader) *Generator {
	return &Generator{db: db, dial: dial}
}

// Tick is a cron.Task.Run function - one pass of generated traffic. Batch
// size scales with the current dial value: ~1-2 rows/tick at baseline
// (10), up to ~11 at max (100).
func (g *Generator) Tick(ctx context.Context) error {
	batch := 1 + g.dial.Get()/10

	if err := g.insertTasks(ctx, batch); err != nil {
		return fmt.Errorf("insert tasks: %w", err)
	}
	if err := g.updateRandomTasks(ctx, batch); err != nil {
		return fmt.Errorf("update tasks: %w", err)
	}
	if err := g.insertComments(ctx, batch); err != nil {
		return fmt.Errorf("insert comments: %w", err)
	}
	if err := g.deleteOldComments(ctx, batch); err != nil {
		return fmt.Errorf("delete comments: %w", err)
	}
	return nil
}

var taskStatuses = []string{"todo", "in_progress", "done", "archived"}

// insertTasks adds n new tasks under randomly picked existing projects,
// each maybe assigned to a randomly picked existing user.
func (g *Generator) insertTasks(ctx context.Context, n int) error {
	projectIDs, err := randomIDs(ctx, g.db, "projects", n)
	if err != nil || len(projectIDs) == 0 {
		return err
	}
	userIDs, err := randomIDs(ctx, g.db, "users", n)
	if err != nil {
		return err
	}

	for i, pid := range projectIDs {
		var assignee any
		if i < len(userIDs) && rand.Float64() < 0.8 {
			assignee = userIDs[i]
		}
		status := taskStatuses[rand.Intn(len(taskStatuses))]
		title := fmt.Sprintf("Traffic task %d", time.Now().UnixNano())

		_, err := g.db.ExecContext(ctx,
			`INSERT INTO tasks (project_id, assignee_id, title, status, metadata)
			 VALUES ($1, $2, $3, $4, '{}')
			 ON CONFLICT (assignee_id) WHERE status = 'in_progress' DO NOTHING`,
			pid, assignee, title, status,
		)
		if err != nil {
			return err
		}
	}
	return nil
}

// updateRandomTasks flips the status on n randomly picked existing tasks.
// Can legitimately collide with the partial-unique "one in_progress task
// per assignee" index - that's just skipped, not an error.
func (g *Generator) updateRandomTasks(ctx context.Context, n int) error {
	ids, err := randomIDs(ctx, g.db, "tasks", n)
	if err != nil {
		return err
	}
	for _, id := range ids {
		status := taskStatuses[rand.Intn(len(taskStatuses))]
		_, err := g.db.ExecContext(ctx, `UPDATE tasks SET status = $1 WHERE id = $2`, status, id)
		if err != nil && !isUniqueViolation(err) {
			return err
		}
	}
	return nil
}

// insertComments adds n new comments on randomly picked existing tasks,
// from randomly picked existing users.
func (g *Generator) insertComments(ctx context.Context, n int) error {
	taskIDs, err := randomIDs(ctx, g.db, "tasks", n)
	if err != nil {
		return err
	}
	userIDs, err := randomIDs(ctx, g.db, "users", n)
	if err != nil {
		return err
	}

	m := min(len(taskIDs), len(userIDs))
	for i := 0; i < m; i++ {
		body := fmt.Sprintf("Auto comment %d", time.Now().UnixNano())
		_, err := g.db.ExecContext(ctx,
			`INSERT INTO task_comments (task_id, user_id, body) VALUES ($1, $2, $3)`,
			taskIDs[i], userIDs[i], body,
		)
		if err != nil {
			return err
		}
	}
	return nil
}

// deleteOldComments removes the n oldest task_comments - a real WHERE
// clause (via a subquery on id), same shape user-submitted DELETEs will
// have to follow later.
func (g *Generator) deleteOldComments(ctx context.Context, n int) error {
	_, err := g.db.ExecContext(ctx,
		`DELETE FROM task_comments WHERE id IN (
			SELECT id FROM task_comments ORDER BY created_at ASC LIMIT $1
		)`,
		n,
	)
	return err
}

// randomIDs pulls a genuinely random sample of n ids from table, fetched
// as one query result set in Go rather than as a per-row correlated
// subquery in SQL - sidesteps the uncorrelated-subquery/InitPlan gotcha
// that bit scripts/seed_data.sql in the main Worm repo (an uncorrelated
// "ORDER BY random() LIMIT 1" subquery gets evaluated once for the whole
// statement, not once per row).
func randomIDs(ctx context.Context, db *sql.DB, table string, n int) ([]string, error) {
	rows, err := db.QueryContext(ctx, fmt.Sprintf("SELECT id FROM %s ORDER BY random() LIMIT %d", table, n))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

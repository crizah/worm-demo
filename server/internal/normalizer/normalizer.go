// Package normalizer keeps every table's row count near its seeded
// baseline: deletes oldest excess if traffic pushed it over, tops it back
// up via seeddata if traffic (or a user-submitted DELETE later) pushed it
// under. Pure DML through the same restricted demo_writer role as
// everything else - never touches the replication slot, publication, or
// the stream itself, so the db and worm's pipeline just keep running.
package normalizer

import (
	"context"
	"database/sql"
	"fmt"

	"golang.org/x/sync/errgroup"

	"server/internal/seeddata"
)

type tableSpec struct {
	table    string
	orderBy  string // column to delete the OLDEST of when over baseline
	baseline int
	topUp    func(context.Context, *sql.DB, int) error
}

// baselines match cmd/seed's initial counts. Tables with no timestamp
// column (the composite-PK junction tables) just use their first PK
// column for ordering - there's no natural "oldest" for them anyway, any
// deterministic pick is fine.
var specs = []tableSpec{
	{"organizations", "created_at", 30, seeddata.SeedOrganizations},
	{"users", "created_at", 500, seeddata.SeedUsers},
	{"teams", "created_at", 100, seeddata.SeedTeams},
	{"tags", "id", 150, seeddata.SeedTags},
	{"roles", "id", 60, seeddata.SeedRoles},
	{"webhooks", "created_at", 80, seeddata.SeedWebhooks},
	{"team_members", "joined_at", 800, seeddata.SeedTeamMembers},
	{"projects", "created_at", 300, seeddata.SeedProjects},
	{"integrations", "connected_at", 60, seeddata.SeedIntegrations},
	{"api_keys", "created_at", 100, seeddata.SeedAPIKeys},
	{"user_roles", "user_id", 700, seeddata.SeedUserRoles},
	{"audit_logs", "created_at", 2000, seeddata.SeedAuditLogs},
	{"webhook_deliveries", "delivered_at", 1500, seeddata.SeedWebhookDeliveries},
	{"milestones", "created_at", 500, seeddata.SeedMilestones},
	{"tasks", "created_at", 8000, seeddata.SeedTasks},
	{"task_tags", "task_id", 12000, seeddata.SeedTaskTags},
	{"task_comments", "created_at", 10000, seeddata.SeedTaskComments},
	{"task_dependencies", "task_id", 3000, seeddata.SeedTaskDependencies},
	{"notifications", "created_at", 6000, seeddata.SeedNotifications},
	{"time_entries", "logged_at", 9000, seeddata.SeedTimeEntries},
}

type Normalizer struct {
	db *sql.DB
}

func New(db *sql.DB) *Normalizer {
	return &Normalizer{db: db}
}

// Tick is a cron.Task.Run function. Every table is independent of the
// others here (all parents already exist from the initial seed), so
// unlike cmd/seed's level-by-level barrier, everything just runs
// concurrently. One known imprecision: deleting from a parent table
// cascades to children, so a table's count can drift slightly off target
// within the same tick if a concurrent parent-table delete cascaded into
// it - self-corrects next hour, not worth serializing for.
func (nm *Normalizer) Tick(ctx context.Context) error {
	g, gctx := errgroup.WithContext(ctx)
	for _, spec := range specs {
		spec := spec
		g.Go(func() error { return nm.normalizeOne(gctx, spec) })
	}
	return g.Wait()
}

func (nm *Normalizer) normalizeOne(ctx context.Context, spec tableSpec) error {
	var count int
	if err := nm.db.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", spec.table)).Scan(&count); err != nil {
		return fmt.Errorf("%s: counting: %w", spec.table, err)
	}

	switch {
	case count > spec.baseline:
		if _, err := nm.trim(ctx, spec, count-spec.baseline); err != nil {
			return fmt.Errorf("%s: trimming: %w", spec.table, err)
		}
	case count < spec.baseline:
		if err := spec.topUp(ctx, nm.db, spec.baseline-count); err != nil {
			return fmt.Errorf("%s: topping up: %w", spec.table, err)
		}
	}
	return nil
}

// trim deletes the oldest n rows via ctid, not id - works the same for
// single-PK tables and the composite-PK junction tables without needing
// to know which shape a given table's key is.
func (nm *Normalizer) trim(ctx context.Context, spec tableSpec, n int) (int64, error) {
	res, err := nm.db.ExecContext(ctx, fmt.Sprintf(
		`DELETE FROM %s WHERE ctid IN (SELECT ctid FROM %s ORDER BY %s ASC LIMIT $1)`,
		spec.table, spec.table, spec.orderBy,
	), n)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

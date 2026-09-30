// Package normalizer periodically trims the tables the traffic generator
// keeps inserting into, so the dataset doesn't grow unbounded on a
// tmpfs-backed Postgres between full resets.
//
// Deliberately DML-only: DELETE with a real WHERE, nothing else. Runs
// through the same restricted demo_writer role as the generator and
// (later) user-submitted SQL (see sql/restricted_role.sql), not a
// privileged one - so it structurally can't do more than that even if
// this code has a bug.
//
// This is NOT the full reseed job from docs/demo-app-plan.md (DROP
// SCHEMA + recreate + re-run migrate-schema/migrate-data). That one needs
// DDL and a privileged role, and resets worm's capture_stage state - it
// belongs in its own separately-privileged job, not here.
package normalizer

import (
	"context"
	"database/sql"
	"fmt"
	"log"
)

// tableCap is a table this job keeps bounded, and how many rows it's
// allowed to hold before the oldest excess gets deleted.
type tableCap struct {
	table   string
	orderBy string // column to delete the OLDEST of, once over cap
	maxRows int
}

// caps lists only the tables the traffic generator inserts into
// unboundedly (task_comments) or grows over time without ever shrinking
// on its own (tasks, audit_logs). The rest only grow via the one-off seed
// script, not ongoing traffic, so they don't need capping here.
var caps = []tableCap{
	{table: "task_comments", orderBy: "created_at", maxRows: 5000},
	{table: "audit_logs", orderBy: "created_at", maxRows: 3000},
	{table: "tasks", orderBy: "created_at", maxRows: 3000},
}

type Normalizer struct {
	db *sql.DB
}

func New(db *sql.DB) *Normalizer {
	return &Normalizer{db: db}
}

// Tick is a cron.Task.Run function.
func (n *Normalizer) Tick(ctx context.Context) error {
	for _, c := range caps {
		removed, err := n.enforceCap(ctx, c)
		if err != nil {
			return fmt.Errorf("capping %s: %w", c.table, err)
		}
		if removed > 0 {
			log.Printf("[normalizer] %s: trimmed %d rows back to cap %d", c.table, removed, c.maxRows)
		}
	}
	return nil
}

func (n *Normalizer) enforceCap(ctx context.Context, c tableCap) (int64, error) {
	var count int
	if err := n.db.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", c.table)).Scan(&count); err != nil {
		return 0, err
	}
	if count <= c.maxRows {
		return 0, nil
	}
	excess := count - c.maxRows

	res, err := n.db.ExecContext(ctx, fmt.Sprintf(
		`DELETE FROM %s WHERE id IN (SELECT id FROM %s ORDER BY %s ASC LIMIT $1)`,
		c.table, c.table, c.orderBy,
	), excess)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

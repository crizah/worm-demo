// Package stats exposes how much write activity postgres has recorded
// against the demo tables - the same activity worm is replicating
// downstream. Reads postgres's own built-in counters, nothing bespoke to
// keep in sync with the traffic generator.
package stats

import (
	"context"
	"database/sql"
)

type Snapshot struct {
	TotalWrites int64 `json:"totalWrites"`
}

func Read(ctx context.Context, db *sql.DB) (Snapshot, error) {
	var total int64
	err := db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(n_tup_ins + n_tup_upd + n_tup_del), 0)
		FROM pg_stat_user_tables
		WHERE schemaname = 'public'
	`).Scan(&total)
	return Snapshot{TotalWrites: total}, err
}

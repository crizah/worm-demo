// Package dbconn opens the single connection this whole backend uses.
package dbconn

import (
	"database/sql"
	"fmt"

	_ "github.com/lib/pq"
)

// Open connects using whatever role the connection string authenticates
// as. In this app that should always be the restricted demo_writer role
// (see sql/restricted_role.sql) - the traffic generator, the normalizer,
// and (later) user-submitted SQL from the web UI all go through this same
// connection/role on purpose, so there's exactly one permission boundary
// to get right, not several. Never point this at a superuser or at the
// role worm's own migrate-resume process uses.
func Open(connStr string) (*sql.DB, error) {
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("opening db: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("pinging db: %w", err)
	}
	return db, nil
}

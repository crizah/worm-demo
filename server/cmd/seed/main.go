// Command seed wipes and rebuilds the demo's source Postgres database:
// applies schema.sql, then seeds it with data. One-time (or occasional
// manual re-run) provisioning tool - not something run on a schedule
// against a live demo. Needs a privileged connection (CREATE rights),
// never the restricted demo_writer role (see sql/restricted_role.sql).
package main

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"log"
	"os"
	"time"

	_ "github.com/lib/pq"
)

//go:embed schema.sql
var schemaSQL string

func main() {
	connStr := os.Getenv("SEED_CONN_STR")
	if connStr == "" {
		log.Fatal("SEED_CONN_STR not set - needs a privileged (DDL-capable) connection, not demo_writer")
	}

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		log.Fatalf("opening db: %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		log.Fatalf("pinging db: %v", err)
	}
	// this whole tool does big concurrent bulk inserts, not lots of tiny
	// ones - a handful of connections is plenty, no need for the pool to
	// grow unbounded across 5 levels x up to 7 tables each
	db.SetMaxOpenConns(16)

	ctx := context.Background()

	log.Print("wiping and reapplying schema...")
	if err := applySchema(ctx, db); err != nil {
		log.Fatalf("applying schema: %v", err)
	}

	start := time.Now()
	if err := seedAll(ctx, db); err != nil {
		log.Fatalf("seeding: %v", err)
	}
	log.Printf("done in %s", time.Since(start))
}

func applySchema(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		return fmt.Errorf("dropping schema: %w", err)
	}
	// lib/pq's simple query protocol runs a `;`-separated file as one call
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("applying schema.sql: %w", err)
	}
	return nil
}

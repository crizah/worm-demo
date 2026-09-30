// Command normalize runs one pass of internal/normalizer and exits -
// meant to be invoked by the machine's own cron, not run continuously.
// See infra/cron/normalize.cron for the crontab line.
package main

import (
	"context"
	"log"
	"os"
	"time"

	"server/internal/dbconn"
	"server/internal/normalizer"
)

func main() {
	connStr := os.Getenv("DEMO_WRITER_CONN_STR")
	if connStr == "" {
		log.Fatal("DEMO_WRITER_CONN_STR not set - this must point at the restricted demo_writer role, see sql/restricted_role.sql")
	}

	db, err := dbconn.Open(connStr)
	if err != nil {
		log.Fatalf("connecting to db: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if err := normalizer.New(db).Tick(ctx); err != nil {
		log.Fatalf("normalize: %v", err)
	}
	log.Print("normalize: done")
}

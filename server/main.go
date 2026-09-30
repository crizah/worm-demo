package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"server/internal/cron"
	"server/internal/dbconn"
	"server/internal/dial"
	"server/internal/normalizer"
	"server/internal/reset"
	"server/internal/traffic"
	"server/internal/wormproc"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	connStr := os.Getenv("DEMO_WRITER_CONN_STR")
	if connStr == "" {
		log.Fatal("DEMO_WRITER_CONN_STR not set - this must point at the restricted demo_writer role, see sql/restricted_role.sql")
	}

	db, err := dbconn.Open(connStr)
	if err != nil {
		log.Fatalf("connecting to db: %v", err)
	}
	defer db.Close()

	dialSvc := dial.NewService()
	go dialSvc.StartDecay(ctx)

	gen := traffic.NewGenerator(db, dialSvc)
	norm := normalizer.New(db)

	wormBin := os.Getenv("WORM_BIN")
	if wormBin == "" {
		log.Fatal("WORM_BIN not set - path to the worm binary")
	}
	wormDir := os.Getenv("WORM_DIR")
	if wormDir == "" {
		log.Fatal("WORM_DIR not set - working directory to run worm in (needs its .env and ./.data alongside it)")
	}
	sup := wormproc.New(wormBin, wormDir)
	resetJob := reset.New(sup)

	sched := cron.New()
	sched.Register(cron.Task{Name: "traffic-generator", Interval: 5 * time.Second, Run: gen.Tick})
	sched.Register(cron.Task{Name: "normalizer", Interval: 5 * time.Minute, Run: norm.Tick})
	sched.Register(cron.Task{Name: "reset", Interval: 2 * time.Hour, RunImmediately: true, Run: resetJob.Tick})
	sched.Start(ctx)

	handlers := dial.NewHandlers(dialSvc)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/traffic/dial", handlers.Get)
	mux.HandleFunc("POST /api/traffic/dial", handlers.Set)

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}
	srv := &http.Server{Addr: addr, Handler: mux}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	log.Printf("listening on %s", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
}

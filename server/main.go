package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"server/internal/dbconn"
	"server/internal/dial"
	"server/internal/middleware"
	"server/internal/traffic"
)

// CORS: edit to add/remove the frontend's domain(s).
var allowedOrigins = map[string]bool{
	"http://localhost:3000": true, // local frontend dev
	// "https://your-demo.vercel.app": true,
}

// Rate-limit bypass - edit to add trusted dev/admin IPs.
var trustedIPs = map[string]bool{
	// "1.2.3.4": true,
}

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
	gen.Start(ctx) // its own continuous rate-limited worker pool - see internal/traffic
	// normalizing runs out-of-process now, via cmd/normalize on the machine's
	// own cron (infra/cron/normalize.cron) - no in-process scheduler needed here.

	handlers := dial.NewHandlers(dialSvc)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/traffic/dial", handlers.Get)
	mux.HandleFunc("POST /api/traffic/dial", handlers.Set)

	rateLimited := middleware.RateLimit(ctx, middleware.RateLimitConfig{
		PerIPRate:    2,
		PerIPBurst:   5,
		GlobalRate:   30,
		GlobalBurst:  50,
		MaxInFlight:  50,
		AllowlistIPs: trustedIPs,
	})(mux)
	var handler http.Handler = middleware.CORS(allowedOrigins)(rateLimited)

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}
	srv := &http.Server{Addr: addr, Handler: handler}

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

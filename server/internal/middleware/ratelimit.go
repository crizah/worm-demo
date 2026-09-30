package middleware

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// RateLimitConfig configures RateLimit.
type RateLimitConfig struct {
	PerIPRate  rate.Limit // requests/sec allowed per visitor IP
	PerIPBurst int

	// GlobalRate/GlobalBurst are the real backstop: per-IP identification
	// alone doesn't hold up against someone cycling IPs specifically to
	// dodge it, so every request also draws from one shared bucket across
	// all visitors combined, regardless of which IP they're on.
	GlobalRate  rate.Limit
	GlobalBurst int

	// MaxInFlight is a separate, complementary guard - a semaphore
	// capping how many requests are being handled AT ONCE, not how many
	// arrive per second. That's a different problem than rate limiting
	// (concurrency vs. throughput) and doesn't replace GlobalRate above;
	// it's insurance against a burst pileup independent of arrival rate.
	MaxInFlight int

	// AllowlistIPs bypass both the per-IP and global limits entirely -
	// edit directly, e.g. for your own dev/admin IP. Set before Start,
	// not safe for concurrent writes once the server's serving requests.
	AllowlistIPs map[string]bool
}

// RateLimit wraps a handler with the per-IP + global + in-flight guards
// described in RateLimitConfig. ctx controls the background cleanup
// goroutine that evicts stale per-IP entries - pass the same ctx the rest
// of the server shuts down on.
func RateLimit(ctx context.Context, cfg RateLimitConfig) func(http.Handler) http.Handler {
	global := rate.NewLimiter(cfg.GlobalRate, cfg.GlobalBurst)
	sem := make(chan struct{}, cfg.MaxInFlight)

	var mu sync.Mutex
	perIP := make(map[string]*visitor)
	go cleanupLoop(ctx, &mu, perIP)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r)

			if cfg.AllowlistIPs[ip] {
				next.ServeHTTP(w, r)
				return
			}

			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			default:
				http.Error(w, "too many requests in flight", http.StatusTooManyRequests)
				return
			}

			if !global.Allow() {
				http.Error(w, "too many requests", http.StatusTooManyRequests)
				return
			}

			mu.Lock()
			v, ok := perIP[ip]
			if !ok {
				v = &visitor{limiter: rate.NewLimiter(cfg.PerIPRate, cfg.PerIPBurst)}
				perIP[ip] = v
			}
			v.lastSeen = time.Now()
			limiter := v.limiter
			mu.Unlock()

			if !limiter.Allow() {
				http.Error(w, "too many requests", http.StatusTooManyRequests)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

type visitor struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// cleanupLoop evicts IPs not seen in a while - perIP only grows otherwise,
// a real concern for a long-running public demo with many visitors.
func cleanupLoop(ctx context.Context, mu *sync.Mutex, perIP map[string]*visitor) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			mu.Lock()
			for ip, v := range perIP {
				if time.Since(v.lastSeen) > 30*time.Minute {
					delete(perIP, ip)
				}
			}
			mu.Unlock()
		}
	}
}

// clientIP prefers X-Forwarded-For over RemoteAddr, which is otherwise
// just Caddy's own address for every request once this sits behind it.
// Trusted here because Caddy - not an arbitrary client - is what's
// actually setting that header in this deployment; if this ever sits
// directly on the internet with no proxy in front, this needs to stop
// trusting it (a client could set it to anything). Also the real, blunt
// limit of IP-based identification generally: it's what's available
// without visitor accounts, not a strong identity - that's exactly why
// GlobalRate exists as a backstop above, not a substitute for it.
func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		if i := strings.IndexByte(fwd, ','); i != -1 {
			return strings.TrimSpace(fwd[:i])
		}
		return strings.TrimSpace(fwd)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

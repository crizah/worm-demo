// Package dial holds the shared, global traffic-dial state: how hard the
// traffic generator should currently be hitting the demo database.
package dial

import (
	"context"
	"sync"
	"time"
)

const (
	Baseline = 100 // resting traffic level, requests/sec
	Max      = 600 // hard server-side ceiling, requests/sec - the UI's own max is only a suggestion, this is the real one

	idleThreshold = 60 * time.Second // no interaction for this long -> start decaying back to Baseline
	decayInterval = 5 * time.Second
	decayStep     = 0.15 // fraction of the remaining gap to Baseline closed per decay tick
)

// Service holds the current shared traffic-dial value. One demo, one
// shared knob - not per visitor, so "watch the whole page react to
// someone else's action" is itself part of the demo. Set clamps into
// [0, Max] regardless of what's asked for. Idle for idleThreshold and the
// value eases back toward Baseline on its own (see StartDecay), so nobody
// can permanently spike load and walk away.
type Service struct {
	mu      sync.Mutex
	value   float64
	lastSet time.Time
}

func NewService() *Service {
	return &Service{value: Baseline, lastSet: time.Now()}
}

// Set clamps v into [0, Max], applies it, and resets the idle clock.
// Returns the value actually applied (post-clamp).
func (s *Service) Set(v int) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	if v < 0 {
		v = 0
	}
	if v > Max {
		v = Max
	}
	s.value = float64(v)
	s.lastSet = time.Now()
	return v
}

// Get returns the current value, rounded for display/use.
func (s *Service) Get() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return int(s.value)
}

// StartDecay runs the idle-decay loop until ctx is cancelled. Launch once,
// e.g. `go dialSvc.StartDecay(ctx)` from main.
func (s *Service) StartDecay(ctx context.Context) {
	ticker := time.NewTicker(decayInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tickDecay()
		}
	}
}

func (s *Service) tickDecay() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if time.Since(s.lastSet) < idleThreshold {
		return // still within the grace period after the last interaction
	}
	if s.value == Baseline {
		return
	}
	s.value += (Baseline - s.value) * decayStep
	if abs(s.value-Baseline) < 0.5 {
		s.value = Baseline // snap - the exponential ease never exactly reaches it otherwise
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

package cron

import (
	"context"
	"log"
	"time"
)

// Task is a named unit of periodic work, run repeatedly on its own
// interval until ctx is cancelled.
type Task struct {
	Name     string
	Interval time.Duration
	// RunImmediately fires the task once as soon as the scheduler starts,
	// instead of waiting for the first tick - a ticker only fires after
	// Interval elapses, so without this a long-interval task (the reset
	// job, at 2h) would leave the demo cold for that whole span on every
	// process start/restart, not just the very first deploy.
	RunImmediately bool
	Run            func(ctx context.Context) error
}

// Scheduler runs a fixed set of interval-based tasks, each on its own
// goroutine/ticker. No cron-expression parsing - these are simple fixed
// intervals, which is all the traffic generator/normalizer need.
type Scheduler struct {
	tasks []Task
}

func New() *Scheduler {
	return &Scheduler{}
}

func (s *Scheduler) Register(t Task) {
	s.tasks = append(s.tasks, t)
}

// Start launches every registered task and returns immediately. Each task
// stops when ctx is cancelled.
func (s *Scheduler) Start(ctx context.Context) {
	for _, t := range s.tasks {
		go s.run(ctx, t)
	}
}

func (s *Scheduler) run(ctx context.Context, t Task) {
	if t.RunImmediately {
		select {
		case <-ctx.Done():
			return
		default:
			if err := t.Run(ctx); err != nil {
				log.Printf("[cron] %s: %v", t.Name, err)
			}
		}
	}

	ticker := time.NewTicker(t.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := t.Run(ctx); err != nil {
				log.Printf("[cron] %s: %v", t.Name, err)
			}
		}
	}
}

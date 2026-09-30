// Package reset implements the periodic full-cycle reset: stop whatever's
// currently streaming, tear down worm's own state (slot, publication,
// state db), rebuild the target schema, and kick off a fresh
// backfill+stream. This is the "start from scratch every N hours" job -
// not the row-capping normalizer (internal/normalizer), which is a
// separate, narrower, DML-only safeguard for the time between resets.
package reset

import (
	"context"
	"fmt"

	"server/internal/wormproc"
)

type Job struct {
	sup *wormproc.Supervisor
}

func New(sup *wormproc.Supervisor) *Job {
	return &Job{sup: sup}
}

// Tick is a cron.Task.Run function.
func (j *Job) Tick(ctx context.Context) error {
	// migrate-reset's slot-drop fails outright if anything is still
	// connected to it, so the current stream has to be stopped first.
	if err := j.sup.Stop(); err != nil {
		return fmt.Errorf("stopping current stream: %w", err)
	}
	if err := j.sup.RunOneShot(ctx, "migrate-reset"); err != nil {
		return fmt.Errorf("migrate-reset: %w", err)
	}
	if err := j.sup.RunOneShot(ctx, "migrate-schema"); err != nil {
		return fmt.Errorf("migrate-schema: %w", err)
	}
	// migrate-data does backfill then falls into streaming forever - start
	// it, don't wait for it. From here it's the new "current stream" the
	// next reset (or an unexpected crash, via the supervisor) will handle.
	if err := j.sup.StartStreaming("migrate-data"); err != nil {
		return fmt.Errorf("starting migrate-data: %w", err)
	}
	return nil
}

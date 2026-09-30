// Package wormproc owns the lifecycle of the one `worm` subprocess that's
// allowed to be actively streaming at a time.
package wormproc

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"sync"
)

// Supervisor runs `worm` subcommands as child processes. One-shot
// commands (migrate-reset, migrate-schema) are run to completion.
// Streaming commands (migrate-data, migrate-resume) are started and left
// running - they never return on their own (they fall into an infinite
// replication loop after backfill) - and are tracked so a later Stop()
// can signal them, or an unexpected exit can trigger an automatic
// restart.
type Supervisor struct {
	bin string
	dir string // working directory worm runs in, so its ./.data/ paths resolve consistently

	mu      sync.Mutex
	cmd     *exec.Cmd
	done    chan struct{} // closed once cmd.Wait() returns
	stopped bool          // true only when Stop() caused the exit - distinguishes that from a crash
}

func New(bin, dir string) *Supervisor {
	return &Supervisor{bin: bin, dir: dir}
}

// RunOneShot runs a worm subcommand to completion and returns its
// combined output on failure. For migrate-reset/migrate-schema.
func (s *Supervisor) RunOneShot(ctx context.Context, subcommand string) error {
	cmd := exec.CommandContext(ctx, s.bin, subcommand)
	cmd.Dir = s.dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w\n%s", subcommand, err, out)
	}
	return nil
}

// StartStreaming launches subcommand ("migrate-data" or "migrate-resume")
// in the background and returns immediately - it does not wait for it.
// Only one streaming process is allowed at a time.
func (s *Supervisor) StartStreaming(subcommand string) error {
	s.mu.Lock()
	if s.cmd != nil {
		s.mu.Unlock()
		return fmt.Errorf("already running a streaming process")
	}

	cmd := exec.Command(s.bin, subcommand)
	cmd.Dir = s.dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		s.mu.Unlock()
		return fmt.Errorf("starting %s: %w", subcommand, err)
	}

	done := make(chan struct{})
	s.cmd = cmd
	s.done = done
	s.stopped = false
	s.mu.Unlock()

	log.Printf("[wormproc] %s started (pid %d)", subcommand, cmd.Process.Pid)
	go s.watch(cmd, done)
	return nil
}

func (s *Supervisor) watch(cmd *exec.Cmd, done chan struct{}) {
	err := cmd.Wait()
	close(done)

	s.mu.Lock()
	stopped := s.stopped
	s.cmd = nil
	s.done = nil
	s.mu.Unlock()

	if stopped {
		return // Stop() caused this - expected, nothing to do
	}

	log.Printf("[wormproc] streaming process exited unexpectedly: %v - restarting with migrate-resume", err)
	if err := s.StartStreaming("migrate-resume"); err != nil {
		log.Printf("[wormproc] failed to restart: %v", err)
	}
}

// Stop signals the current streaming process (if any) and waits for it to
// actually exit before returning. Safe to call when nothing is running.
func (s *Supervisor) Stop() error {
	s.mu.Lock()
	cmd := s.cmd
	done := s.done
	if cmd == nil {
		s.mu.Unlock()
		return nil
	}
	s.stopped = true
	s.mu.Unlock()

	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		return fmt.Errorf("signaling process: %w", err)
	}
	<-done // wait for the watch() goroutine's Wait() to actually finish - never call Wait() twice on the same *exec.Cmd
	return nil
}

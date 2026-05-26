// Package uisupervisor keeps a child process alive on behalf of the collector
// (ADR 0001): the collector owns the UI process lifecycle, so it spawns the UI
// on demand, respawns it after an unexpected exit, and terminates it on
// shutdown. The supervisor is transport- and platform-agnostic; the collector
// supplies the command to run.
package uisupervisor

import (
	"context"
	"errors"
	"log/slog"
	"os/exec"
	"time"

	"github.com/dusthoff/hashpoint/internal/crashguard"
)

const (
	defaultMinBackoff = 200 * time.Millisecond
	defaultMaxBackoff = 10 * time.Second
	// healthyRuntime is how long a child must run before its launch counts as
	// healthy and the restart backoff resets. Quicker exits are treated as a
	// crash loop, so the backoff grows to avoid hammering a wedged UI.
	healthyRuntime = 5 * time.Second
)

// Supervisor restarts a child process until its context is cancelled.
type Supervisor struct {
	newCmd     func(context.Context) *exec.Cmd
	logger     *slog.Logger
	minBackoff time.Duration
	maxBackoff time.Duration
}

// Option configures a Supervisor.
type Option func(*Supervisor)

// WithBackoff overrides the restart backoff bounds (mainly for tests).
func WithBackoff(minBackoff, maxBackoff time.Duration) Option {
	return func(s *Supervisor) { s.minBackoff, s.maxBackoff = minBackoff, maxBackoff }
}

// New builds a supervisor that (re)starts the command returned by newCmd.
// newCmd must build the command with the supplied context (e.g. via
// exec.CommandContext) so a cancelled context terminates the running child.
func New(newCmd func(context.Context) *exec.Cmd, logger *slog.Logger, opts ...Option) *Supervisor {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Supervisor{
		newCmd:     newCmd,
		logger:     logger,
		minBackoff: defaultMinBackoff,
		maxBackoff: defaultMaxBackoff,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Run blocks, keeping the child alive until ctx is cancelled, then returns
// ctx.Err(). A child that exits while ctx is still live is respawned after a
// backoff that grows on rapid repeated failures and resets once a launch
// survives healthyRuntime.
func (s *Supervisor) Run(ctx context.Context) error {
	backoff := s.minBackoff
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		started := time.Now()
		s.runOnce(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Since(started) >= healthyRuntime {
			backoff = s.minBackoff
		}
		s.logger.Warn("ui: process exited — respawning", "backoff", backoff)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > s.maxBackoff {
			backoff = s.maxBackoff
		}
	}
}

// runOnce starts the child and waits for it to exit. A cancelled ctx
// terminates the child via the command's context, so Wait returns promptly on
// shutdown; that exit is expected and not logged. An unexpected exit (ctx still
// live) is recorded as a UI crash with the exit code and how long the child ran,
// so the surviving collector captures it in the log even though the UI process
// has no log file of its own.
func (s *Supervisor) runOnce(ctx context.Context) {
	cmd := s.newCmd(ctx)
	started := time.Now()
	if err := cmd.Start(); err != nil {
		s.logger.Warn("ui: start failed", "err", err)
		return
	}
	s.logger.Info("ui: started", "pid", cmd.Process.Pid)
	err := cmd.Wait()
	if ctx.Err() != nil {
		return // expected: our own shutdown terminated the child
	}
	runtimeSec := int(time.Since(started).Seconds())
	if err == nil {
		s.logger.Warn("ui: process exited cleanly but unexpectedly", "runtime_sec", runtimeSec)
		return
	}
	exitCode := -1
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		exitCode = ee.ExitCode()
	}
	s.logger.Error("ui process crashed",
		"event", crashguard.EventUICrash,
		"exit_code", exitCode,
		"runtime_sec", runtimeSec,
		"err", err)
}

//go:build windows

package uisupervisor

import (
	"bytes"
	"context"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// syncBuf is a goroutine-safe buffer so a test can read the supervisor's log
// output while the supervisor goroutine keeps writing to it.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// TestSupervisor_RespawnsAndStops checks the two contractual behaviours: a
// child that exits is respawned, and cancelling the context ends the loop. It
// waits on a spawn signal rather than wall-clock sleeping so Windows process-
// creation latency cannot make it flaky.
func TestSupervisor_RespawnsAndStops(t *testing.T) {
	var spawns int32
	spawned := make(chan struct{}, 8)
	sup := New(func(ctx context.Context) *exec.Cmd {
		atomic.AddInt32(&spawns, 1)
		select {
		case spawned <- struct{}{}:
		default:
		}
		// Exits immediately, forcing the respawn path on every iteration.
		return exec.CommandContext(ctx, "cmd", "/c", "exit", "0")
	}, nil, WithBackoff(5*time.Millisecond, 5*time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()

	// Two spawns proves the child is respawned after it exits.
	for i := 0; i < 2; i++ {
		select {
		case <-spawned:
		case <-time.After(5 * time.Second):
			cancel()
			<-done
			t.Fatalf("supervisor produced only %d spawns, want >= 2", atomic.LoadInt32(&spawns))
		}
	}

	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Error("Run should return the context error after cancel")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

// TestSupervisor_LogsUICrashOnNonZeroExit checks that an unexpected non-zero
// child exit is recorded as a ui_crash with the exit code — the path that lets
// the surviving collector capture a UI crash in the log.
func TestSupervisor_LogsUICrashOnNonZeroExit(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	spawned := make(chan struct{}, 4)
	first := true
	sup := New(func(ctx context.Context) *exec.Cmd {
		select {
		case spawned <- struct{}{}:
		default:
		}
		if first {
			first = false
			return exec.CommandContext(ctx, "cmd", "/c", "exit", "3")
		}
		// Later spawns block until cancel so the loop does not crash-loop.
		return exec.CommandContext(ctx, "cmd", "/c", "ping", "-n", "60", "127.0.0.1")
	}, logger, WithBackoff(5*time.Millisecond, 5*time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()

	// First (crashing) spawn, then the respawn — by which point the crash has
	// been logged.
	<-spawned
	select {
	case <-spawned:
	case <-time.After(5 * time.Second):
	}
	cancel()
	<-done

	out := buf.String()
	if !strings.Contains(out, "ui_crash") {
		t.Errorf("expected ui_crash log, got: %q", out)
	}
	if !strings.Contains(out, `"exit_code":3`) {
		t.Errorf("expected exit_code 3, got: %q", out)
	}
}

// TestSupervisor_SpawnGate_HoldsUntilOpen verifies the spawn gate keeps the
// child from starting until it returns nil, then a spawn follows.
func TestSupervisor_SpawnGate_HoldsUntilOpen(t *testing.T) {
	var allow atomic.Bool
	spawned := make(chan struct{}, 4)
	sup := New(func(ctx context.Context) *exec.Cmd {
		select {
		case spawned <- struct{}{}:
		default:
		}
		// Long-lived so the gate, not a respawn, is what we observe.
		return exec.CommandContext(ctx, "cmd", "/c", "ping", "-n", "60", "127.0.0.1")
	}, nil, WithBackoff(5*time.Millisecond, 5*time.Millisecond),
		WithSpawnGate(func(ctx context.Context) error {
			for !allow.Load() {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(5 * time.Millisecond):
				}
			}
			return nil
		}))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()

	// Gate closed: nothing spawns.
	select {
	case <-spawned:
		cancel()
		<-done
		t.Fatal("child spawned while the gate was closed")
	case <-time.After(150 * time.Millisecond):
	}

	// Open the gate: a spawn must follow.
	allow.Store(true)
	select {
	case <-spawned:
	case <-time.After(5 * time.Second):
		cancel()
		<-done
		t.Fatal("child did not spawn after the gate opened")
	}

	cancel()
	<-done
}

// TestSupervisor_SpawnGate_CancelEndsRun verifies cancelling the context while
// the gate blocks ends Run without ever spawning.
func TestSupervisor_SpawnGate_CancelEndsRun(t *testing.T) {
	spawned := make(chan struct{}, 1)
	sup := New(func(ctx context.Context) *exec.Cmd {
		select {
		case spawned <- struct{}{}:
		default:
		}
		return exec.CommandContext(ctx, "cmd", "/c", "exit", "0")
	}, nil, WithSpawnGate(func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Error("Run should return the context error when cancelled during the gate")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel during the gate")
	}
	select {
	case <-spawned:
		t.Error("child spawned despite the gate never opening")
	default:
	}
}

// TestSupervisor_CrashLoop_ExtendsBackoff verifies that repeated rapid exits
// trip the crash-loop backoff path.
func TestSupervisor_CrashLoop_ExtendsBackoff(t *testing.T) {
	buf := &syncBuf{}
	logger := slog.New(slog.NewJSONHandler(buf, nil))
	sup := New(func(ctx context.Context) *exec.Cmd {
		// Exits immediately every time, well under healthyRuntime.
		return exec.CommandContext(ctx, "cmd", "/c", "exit", "1")
	}, logger, WithBackoff(2*time.Millisecond, 4*time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()

	// Wait until the crash loop trips (crashLoopThreshold rapid exits). Once it
	// does the wait jumps to crashLoopBackoff, but that select honours ctx, so
	// cancel returns promptly.
	deadline := time.After(10 * time.Second)
	for !strings.Contains(buf.String(), "crash loop detected") {
		select {
		case <-deadline:
			cancel()
			<-done
			t.Fatalf("crash-loop backoff never tripped; log: %q", buf.String())
		case <-time.After(20 * time.Millisecond):
		}
	}
	cancel()
	<-done
}

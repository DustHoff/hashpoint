//go:build windows

package uisupervisor

import (
	"context"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"
)

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

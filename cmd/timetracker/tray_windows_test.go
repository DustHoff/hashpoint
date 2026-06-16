//go:build windows

package main

import (
	"context"
	"testing"
	"time"
)

// TestWaitTrayOutcome covers the decision runTray makes from the tray's
// readiness/teardown signals. The phase-2 cases synchronise via an unbuffered
// send on ready so the goroutine is provably past phase 1 before the next
// signal fires — no sleeps, race-detector clean.
func TestWaitTrayOutcome(t *testing.T) {
	const longTimeout = 10 * time.Second

	t.Run("never ready times out and requests restart", func(t *testing.T) {
		got := waitTrayOutcome(context.Background(), make(chan struct{}), make(chan struct{}), 10*time.Millisecond)
		if got == "" {
			t.Fatal("expected a restart reason on readiness timeout, got clean exit")
		}
	})

	t.Run("cancel before ready is a clean exit", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		got := waitTrayOutcome(ctx, make(chan struct{}), make(chan struct{}), longTimeout)
		if got != "" {
			t.Fatalf("expected clean exit on pre-ready cancel, got %q", got)
		}
	})

	t.Run("loop ends before ready requests restart", func(t *testing.T) {
		done := make(chan struct{})
		close(done)
		got := waitTrayOutcome(context.Background(), make(chan struct{}), done, longTimeout)
		if got == "" {
			t.Fatal("expected a restart reason when the loop ends before ready")
		}
	})

	t.Run("teardown after ready requests restart", func(t *testing.T) {
		ready := make(chan struct{})
		done := make(chan struct{})
		res := make(chan string, 1)
		go func() { res <- waitTrayOutcome(context.Background(), ready, done, longTimeout) }()
		ready <- struct{}{} // synchronise: goroutine is now past phase 1
		close(done)
		if got := <-res; got == "" {
			t.Fatal("expected a restart reason on unexpected teardown")
		}
	})

	t.Run("shutdown after ready is a clean exit", func(t *testing.T) {
		ready := make(chan struct{})
		done := make(chan struct{})
		ctx, cancel := context.WithCancel(context.Background())
		res := make(chan string, 1)
		go func() { res <- waitTrayOutcome(ctx, ready, done, longTimeout) }()
		ready <- struct{}{} // goroutine is now in phase 2
		cancel()
		close(done) // the phase-2 ctx.Done path drains done
		if got := <-res; got != "" {
			t.Fatalf("expected clean exit on shutdown, got %q", got)
		}
	})
}

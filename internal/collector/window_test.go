package collector

import (
	"context"
	"testing"
)

// TestHeadlessWindowControllerPublishesControlEvents verifies each window
// intent maps to the right hub control event (so the UI process can act on it)
// and that Quit triggers the collector's shutdown hook rather than touching a
// Wails runtime (issue #28).
func TestHeadlessWindowControllerPublishesControlEvents(t *testing.T) {
	hub := NewEventHub(8)
	ch, cancel := hub.Subscribe()
	defer cancel()

	quit := 0
	c := NewHeadlessWindowController(hub, func() { quit++ })

	c.ShowMain(context.Background())
	c.OpenQuickTag(context.Background())
	c.CloseQuickTag(context.Background())

	want := []string{EventShowUI, EventQuickTagEnter, EventQuickTagLeave}
	for i, w := range want {
		select {
		case ev := <-ch:
			if ev.Name != w {
				t.Fatalf("event %d = %q, want %q", i, ev.Name, w)
			}
		default:
			t.Fatalf("expected control event %q, none published", w)
		}
	}

	// NoteMainHidden is collector-side bookkeeping only: it must publish nothing.
	c.NoteMainHidden()
	select {
	case ev := <-ch:
		t.Fatalf("NoteMainHidden published %q, want nothing", ev.Name)
	default:
	}

	c.Quit(context.Background())
	if quit != 1 {
		t.Fatalf("quit hook called %d times, want 1", quit)
	}
}

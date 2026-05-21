package collector

import (
	"context"
	"testing"

	"github.com/dusthoff/hashpoint/internal/ipc"
	"github.com/dusthoff/hashpoint/internal/ipc/collectorpb"
)

func TestEventHub_PublishFanout(t *testing.T) {
	t.Parallel()
	h := NewEventHub(8)
	ch1, cancel1 := h.Subscribe()
	defer cancel1()
	ch2, cancel2 := h.Subscribe()
	defer cancel2()

	h.Publish("tick", []byte(`{"n":1}`))

	for i, ch := range []<-chan Event{ch1, ch2} {
		select {
		case ev := <-ch:
			if ev.Name != "tick" || string(ev.JSON) != `{"n":1}` {
				t.Errorf("sub %d: got %+v", i, ev)
			}
		default:
			t.Errorf("sub %d: no event delivered", i)
		}
	}
}

func TestEventHub_DropsWhenFull(t *testing.T) {
	t.Parallel()
	h := NewEventHub(1) // buffer holds exactly one undrained event
	ch, cancel := h.Subscribe()
	defer cancel()

	h.Publish("a", nil) // buffered
	h.Publish("b", nil) // buffer full, no reader → dropped

	if got := h.Dropped(); got != 1 {
		t.Fatalf("dropped = %d, want 1", got)
	}
	if ev := <-ch; ev.Name != "a" {
		t.Errorf("first event = %q, want a", ev.Name)
	}
}

func TestEventHub_UnsubscribeClosesChannel(t *testing.T) {
	t.Parallel()
	h := NewEventHub(4)
	ch, cancel := h.Subscribe()
	cancel()
	if _, ok := <-ch; ok {
		t.Error("channel should be closed after unsubscribe")
	}
	// Publishing after the last unsubscribe must not panic.
	h.Publish("x", nil)
}

func TestService_ShowUI(t *testing.T) {
	t.Parallel()
	hub := NewEventHub(4)
	ch, cancel := hub.Subscribe()
	defer cancel()
	svc := NewService(VersionInfo{}, hub, nil)

	if _, err := svc.ShowUI(context.Background(), &collectorpb.ShowUIRequest{}); err != nil {
		t.Fatalf("ShowUI: %v", err)
	}
	select {
	case ev := <-ch:
		if ev.Name != EventShowUI {
			t.Errorf("published %q, want %q", ev.Name, EventShowUI)
		}
	default:
		t.Error("ShowUI did not publish the show-window event")
	}
}

func TestService_GetVersion(t *testing.T) {
	t.Parallel()
	s := NewService(VersionInfo{Version: "1.2.3", Commit: "abc123"}, NewEventHub(0), nil)
	resp, err := s.GetVersion(context.Background(), &collectorpb.GetVersionRequest{})
	if err != nil {
		t.Fatalf("GetVersion: %v", err)
	}
	if resp.Version != "1.2.3" || resp.Commit != "abc123" {
		t.Errorf("metadata = %+v", resp)
	}
	if resp.ProtocolVersion != ipc.ProtocolVersion {
		t.Errorf("protocol version = %d, want %d", resp.ProtocolVersion, ipc.ProtocolVersion)
	}
}

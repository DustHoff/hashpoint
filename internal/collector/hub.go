// Package collector hosts the headless side of the collector/UI split
// (ADR 0001): the CollectorService implementation the UI reaches over the
// named-pipe transport, plus the event hub that fans domain events out to the
// connected UI. It owns no transport details — internal/ipc serves it — and no
// window/Wails state, which stays in the UI process.
package collector

import "sync"

// Event is a collector-side notification destined for the UI's local event
// bus. Name matches the wails event constant; JSON is the marshalled payload,
// carried opaquely during the migration (ADR Anhang B).
type Event struct {
	Name string
	JSON []byte
}

// EventHub fans collector events out to every connected UI subscriber. Domain
// code publishes; each subscriber — one per open CollectorService.Events
// stream — receives on its own buffered channel. A subscriber that cannot keep
// up has events dropped rather than blocking the publisher: the collector must
// never stall domain work on a slow or wedged UI. Drops are counted for
// diagnostics.
type EventHub struct {
	bufferSize int

	mu      sync.Mutex
	subs    map[int]chan Event
	nextID  int
	dropped uint64
}

// NewEventHub returns a hub whose per-subscriber channels buffer bufferSize
// events. A non-positive bufferSize falls back to a small default.
func NewEventHub(bufferSize int) *EventHub {
	if bufferSize <= 0 {
		bufferSize = 64
	}
	return &EventHub{bufferSize: bufferSize, subs: make(map[int]chan Event)}
}

// Subscribe registers a new subscriber and returns its receive channel plus an
// unsubscribe function. The channel is closed by unsubscribe; callers must call
// it (typically via defer) when their stream ends. Unsubscribe is idempotent.
func (h *EventHub) Subscribe() (<-chan Event, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	id := h.nextID
	h.nextID++
	ch := make(chan Event, h.bufferSize)
	h.subs[id] = ch
	return ch, func() { h.unsubscribe(id) }
}

func (h *EventHub) unsubscribe(id int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if ch, ok := h.subs[id]; ok {
		delete(h.subs, id)
		close(ch)
	}
}

// Publish delivers an event to every current subscriber without blocking. A
// subscriber whose buffer is full has this event dropped (counted), so a slow
// UI can never stall the publishing domain goroutine.
func (h *EventHub) Publish(name string, json []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ev := Event{Name: name, JSON: json}
	for _, ch := range h.subs {
		select {
		case ch <- ev:
		default:
			h.dropped++
		}
	}
}

// Dropped returns the cumulative number of events dropped because a
// subscriber's buffer was full.
func (h *EventHub) Dropped() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.dropped
}

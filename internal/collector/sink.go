package collector

import (
	"context"
	"encoding/json"

	"github.com/dusthoff/hashpoint/internal/app"
)

// eventSink adapts the EventHub to app.EventSink: it marshals each emitted
// payload to JSON and publishes it on the hub for delivery over the UI's
// Events stream (ADR Anhang B). The collector injects it into app.Deps so the
// domain's EventsEmit sites reach the connected UI instead of a (non-existent)
// Wails runtime. ctx is unused — the hub fan-out is non-blocking.
type eventSink struct{ hub *EventHub }

// NewEventSink returns an app.EventSink that publishes events onto hub.
func NewEventSink(hub *EventHub) app.EventSink {
	return eventSink{hub: hub}
}

// Emit marshals the payload to JSON and publishes it on the hub for the UI.
func (s eventSink) Emit(_ context.Context, name string, payload ...any) {
	var data []byte
	if len(payload) > 0 && payload[0] != nil {
		if b, err := json.Marshal(payload[0]); err == nil {
			data = b
		}
	}
	s.hub.Publish(name, data)
}

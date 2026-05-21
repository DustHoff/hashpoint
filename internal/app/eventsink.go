package app

import (
	"context"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// EventSink delivers a backend event to the frontend. In the monolith and the
// UI process it forwards to the Wails runtime; in the collector process it is
// replaced by a sink that publishes onto the IPC event stream (ADR Anhang B),
// since the collector has no Wails runtime of its own. Callers keep their own
// readiness/ctx guards and pass the same (name, payload) they always have.
type EventSink interface {
	Emit(ctx context.Context, name string, payload ...any)
}

// wailsSink is the default sink: a thin pass-through to the Wails runtime that
// preserves the exact emit semantics the app had before the collector/UI
// split, including the no-payload events (variadic payload).
type wailsSink struct{}

func (wailsSink) Emit(ctx context.Context, name string, payload ...any) {
	wailsruntime.EventsEmit(ctx, name, payload...)
}

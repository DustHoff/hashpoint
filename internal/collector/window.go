package collector

import (
	"context"

	"github.com/dusthoff/hashpoint/internal/app"
)

// headlessWindowController is the app.WindowController used by the collector
// process, which owns no Wails window (ADR 0001). Each window intent is
// published as a control event for the connected UI process to act on against
// its own Wails context; the quit intent cancels the collector so the whole
// app shuts down. It never touches a Wails runtime — doing so from the
// collector's non-Wails context would log.Fatalf → os.Exit (issue #28).
type headlessWindowController struct {
	hub  *EventHub
	quit func()
}

// NewHeadlessWindowController returns the collector-side window controller.
// quit is the collector's shutdown trigger, invoked on a Quit intent; it may
// be nil, in which case Quit is a no-op.
func NewHeadlessWindowController(hub *EventHub, quit func()) app.WindowController {
	return headlessWindowController{hub: hub, quit: quit}
}

// ShowMain asks the UI to foreground its window.
func (c headlessWindowController) ShowMain(context.Context) { c.hub.Publish(EventShowUI, nil) }

// OpenQuickTag asks the UI to shrink its window into the quick-tag popup.
func (c headlessWindowController) OpenQuickTag(context.Context) {
	c.hub.Publish(EventQuickTagEnter, nil)
}

// CloseQuickTag asks the UI to restore its window after the quick-tag popup.
func (c headlessWindowController) CloseQuickTag(context.Context) {
	c.hub.Publish(EventQuickTagLeave, nil)
}

// NoteMainHidden is a no-op in the collector: the UI process tracks its own
// window visibility (the collector never sees the Wails OnBeforeClose hook).
func (c headlessWindowController) NoteMainHidden() {}

// Quit shuts the collector down, which tears down the supervised UI with it.
func (c headlessWindowController) Quit(context.Context) {
	if c.quit != nil {
		c.quit()
	}
}

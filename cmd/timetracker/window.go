package main

import (
	"context"
	"log/slog"
	"sync"

	"github.com/dusthoff/hashpoint/internal/winapi"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// Quick-tag-picker geometry (physical pixels) for the popup window the global
// hotkey raises. Compact on purpose: the popup is keyboard-driven and lists at
// most a handful of entries. These live in the windowed half of the split (the
// monolith and the UI shell) because only a process with a Wails runtime sizes
// the window — the headless collector merely forwards the open/close intents.
const (
	quickTagPickerWidth  = 340
	quickTagPickerHeight = 420
	quickTagPickerMargin = 12
)

// popupWindow runs the quick-tag popup choreography against a live Wails
// context and tracks the main window's visibility so closing the popup
// restores the prior state. The same logic backs the monolith's in-process
// window and the split's UI shell; only the context differs. It must call the
// Wails runtime only with a real lifecycle context — that is guaranteed by its
// callers (the monolith's app context, the UI's OnStartup context). All methods
// are safe for concurrent use: the Wails OnBeforeClose hook and the collector
// event pump touch it from different goroutines.
type popupWindow struct {
	logger *slog.Logger

	mu         sync.Mutex
	visible    bool // main window currently visible
	active     bool // popup currently showing (placement saved below)
	wasVisible bool // main-window visibility captured when the popup opened
	x, y, w, h int  // main-window placement captured when the popup opened
}

// newPopupWindow returns a popupWindow assuming the window starts visible — the
// monolith opens Maximised at Startup and the UI shell is a Maximised window.
// A tray-hide or window-close flips that via noteHidden.
func newPopupWindow(logger *slog.Logger) *popupWindow {
	if logger == nil {
		logger = slog.Default()
	}
	return &popupWindow{logger: logger, visible: true}
}

// noteShown / noteHidden record main-window visibility transitions that happen
// outside the popup flow (tray "open", Wails OnBeforeClose).
func (p *popupWindow) noteShown()  { p.mu.Lock(); p.visible = true; p.mu.Unlock() }
func (p *popupWindow) noteHidden() { p.mu.Lock(); p.visible = false; p.mu.Unlock() }

// enter saves the current placement (once per popup session) and shrinks the
// window into the popup anchored at the cursor monitor's bottom-right. A second
// enter while already in popup mode only re-anchors, matching the monolith's
// original idempotent behaviour.
func (p *popupWindow) enter(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.active {
		p.w, p.h = wailsruntime.WindowGetSize(ctx)
		p.x, p.y = wailsruntime.WindowGetPosition(ctx)
		p.wasVisible = p.visible
		p.active = true
	}
	if work, err := winapi.CursorMonitorWorkArea(); err == nil {
		px := int(work.Right) - quickTagPickerWidth - quickTagPickerMargin
		py := int(work.Bottom) - quickTagPickerHeight - quickTagPickerMargin
		wailsruntime.WindowSetPosition(ctx, px, py)
	} else {
		p.logger.Warn("quick tag: cursor monitor lookup failed", "err", err)
		wailsruntime.WindowCenter(ctx)
	}
	wailsruntime.WindowSetSize(ctx, quickTagPickerWidth, quickTagPickerHeight)
	wailsruntime.WindowSetAlwaysOnTop(ctx, true)
	wailsruntime.WindowShow(ctx)
	wailsruntime.WindowUnminimise(ctx)
	p.visible = true
}

// leave restores the placement saved by enter and hides the window again when
// it was hidden before the popup took over, so triggering the picker from the
// tray returns to the tray rather than leaving the full window up.
func (p *popupWindow) leave(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()
	wailsruntime.WindowSetAlwaysOnTop(ctx, false)
	if p.active {
		wailsruntime.WindowSetSize(ctx, p.w, p.h)
		wailsruntime.WindowSetPosition(ctx, p.x, p.y)
	}
	if !p.wasVisible {
		wailsruntime.WindowHide(ctx)
		p.visible = false
	} else {
		p.visible = true
	}
	p.active = false
}

// windowedWindowController is the app.WindowController for processes that own
// the Wails window — the monolith. It drives the in-process window via the
// Wails runtime using the app's live context. (The UI shell of the split runs
// the same popupWindow logic directly from its event pump, not through this
// controller, because its window intents arrive as collector events.)
type windowedWindowController struct {
	popup *popupWindow
}

// newWindowedWindowController builds the monolith's window controller.
func newWindowedWindowController(logger *slog.Logger) *windowedWindowController {
	return &windowedWindowController{popup: newPopupWindow(logger)}
}

// ShowMain brings the main window to the foreground.
func (c *windowedWindowController) ShowMain(ctx context.Context) {
	wailsruntime.WindowShow(ctx)
	wailsruntime.WindowUnminimise(ctx)
	c.popup.noteShown()
}

// Quit triggers a graceful Wails shutdown so OnShutdown runs.
func (c *windowedWindowController) Quit(ctx context.Context) { wailsruntime.Quit(ctx) }

// OpenQuickTag shrinks the window into the quick-tag popup.
func (c *windowedWindowController) OpenQuickTag(ctx context.Context) { c.popup.enter(ctx) }

// CloseQuickTag restores the window after the quick-tag popup.
func (c *windowedWindowController) CloseQuickTag(ctx context.Context) { c.popup.leave(ctx) }

// NoteMainHidden records that the window was hidden (Wails OnBeforeClose).
func (c *windowedWindowController) NoteMainHidden() { c.popup.noteHidden() }

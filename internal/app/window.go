package app

import "context"

// WindowController performs window-level actions on behalf of the app. It
// abstracts the Wails runtime so the domain never calls wailsruntime directly:
// in the collector/UI split (ADR 0001) the App runs headless in the collector
// process, where a.ctx is a plain signal context with no Wails frontend.
// Calling wailsruntime.Window* / Quit with such a context hits Wails'
// getFrontend → log.Fatalf → os.Exit(1) — an unrecoverable crash that no
// crashguard recover can catch (issue #28).
//
// The monolith injects a controller that drives the in-process Wails window;
// the collector injects one that forwards each intent to the UI process over
// the IPC event stream, where the UI shell acts on it against its own Wails
// context. Methods that touch the live window take the app's context; the
// pure-bookkeeping NoteMainHidden does not.
type WindowController interface {
	// ShowMain brings the main window to the foreground.
	ShowMain(ctx context.Context)
	// Quit triggers a graceful shutdown of the windowed application.
	Quit(ctx context.Context)
	// OpenQuickTag saves the current window placement and shrinks the window
	// into the quick-tag popup anchored at the cursor monitor's corner.
	OpenQuickTag(ctx context.Context)
	// CloseQuickTag restores the placement saved by OpenQuickTag.
	CloseQuickTag(ctx context.Context)
	// NoteMainHidden records that the main window was hidden (e.g. the Wails
	// OnBeforeClose hook) so a later quick-tag popup restores the prior state.
	NoteMainHidden()
}

// nopWindowController is the default when no controller is injected (unit
// tests). Every method is a no-op so window-touching app methods stay safe to
// call without a Wails runtime.
type nopWindowController struct{}

// ShowMain is a no-op.
func (nopWindowController) ShowMain(context.Context) {}

// Quit is a no-op.
func (nopWindowController) Quit(context.Context) {}

// OpenQuickTag is a no-op.
func (nopWindowController) OpenQuickTag(context.Context) {}

// CloseQuickTag is a no-op.
func (nopWindowController) CloseQuickTag(context.Context) {}

// NoteMainHidden is a no-op.
func (nopWindowController) NoteMainHidden() {}

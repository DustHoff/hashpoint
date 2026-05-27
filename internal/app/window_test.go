package app

import (
	"context"
	"log/slog"
	"slices"
	"testing"
)

// fakeWindow records the WindowController intents the app issues so the tests
// can assert delegation without a Wails runtime.
type fakeWindow struct {
	showMain, quit, openQuickTag, closeQuickTag, noteHidden int
}

func (f *fakeWindow) ShowMain(context.Context)      { f.showMain++ }
func (f *fakeWindow) Quit(context.Context)          { f.quit++ }
func (f *fakeWindow) OpenQuickTag(context.Context)  { f.openQuickTag++ }
func (f *fakeWindow) CloseQuickTag(context.Context) { f.closeQuickTag++ }
func (f *fakeWindow) NoteMainHidden()               { f.noteHidden++ }

// recordingSink captures the frontend event names the app emits.
type recordingSink struct{ events []string }

func (s *recordingSink) Emit(_ context.Context, name string, _ ...any) {
	s.events = append(s.events, name)
}

// newStartedApp builds an app wired to the fakes and marks it as past Wails
// Startup so the window methods run their real bodies.
func newStartedApp(t *testing.T) (*App, *fakeWindow, *recordingSink) {
	t.Helper()
	win := &fakeWindow{}
	sink := &recordingSink{}
	a := New(Deps{Window: win, Sink: sink, Logger: slog.Default()})
	a.started = true
	a.ctx = context.Background()
	return a, win, sink
}

// TestFireQuickTagToggles verifies the hotkey entry point opens the popup on
// the first press and closes it on the second, delegating the window work to
// the controller and emitting the matching frontend events — the exact path
// that used to call wailsruntime in the collector and crash (issue #28).
func TestFireQuickTagToggles(t *testing.T) {
	a, win, sink := newStartedApp(t)

	a.FireQuickTag()
	if win.openQuickTag != 1 || win.closeQuickTag != 0 || !a.quickTagOpen {
		t.Fatalf("after open: open=%d close=%d quickTagOpen=%v", win.openQuickTag, win.closeQuickTag, a.quickTagOpen)
	}

	a.FireQuickTag()
	if win.openQuickTag != 1 || win.closeQuickTag != 1 || a.quickTagOpen {
		t.Fatalf("after close: open=%d close=%d quickTagOpen=%v", win.openQuickTag, win.closeQuickTag, a.quickTagOpen)
	}

	want := []string{quickTagOpenEvent, quickTagCloseEvent}
	if !slices.Equal(sink.events, want) {
		t.Fatalf("events = %v, want %v", sink.events, want)
	}
}

// TestQuickTagOpenBeforeStartup ensures the popup is not opened before Wails
// Startup has handed the app a context — the controller must stay untouched.
func TestQuickTagOpenBeforeStartup(t *testing.T) {
	win := &fakeWindow{}
	a := New(Deps{Window: win, Sink: &recordingSink{}, Logger: slog.Default()})

	if err := a.QuickTagOpen(); err == nil {
		t.Fatal("QuickTagOpen before Startup should error")
	}
	if win.openQuickTag != 0 {
		t.Fatalf("controller touched before Startup: open=%d", win.openQuickTag)
	}
}

// TestQuickTagDismissCloses verifies dismissing the picker closes the popup
// window and clears the open flag.
func TestQuickTagDismissCloses(t *testing.T) {
	a, win, _ := newStartedApp(t)
	if err := a.QuickTagOpen(); err != nil {
		t.Fatalf("QuickTagOpen: %v", err)
	}

	a.QuickTagDismiss()
	if win.closeQuickTag != 1 || a.quickTagOpen {
		t.Fatalf("after dismiss: close=%d quickTagOpen=%v", win.closeQuickTag, a.quickTagOpen)
	}
}

// TestWindowMethodsDelegate verifies the remaining window-touching app methods
// route through the controller instead of the Wails runtime.
func TestWindowMethodsDelegate(t *testing.T) {
	a, win, _ := newStartedApp(t)

	a.ShowWindow()
	if win.showMain != 1 {
		t.Fatalf("ShowMain = %d, want 1", win.showMain)
	}

	if a.OnWindowBeforeClose(context.Background()) {
		t.Fatal("OnWindowBeforeClose should not prevent close")
	}
	if win.noteHidden != 1 {
		t.Fatalf("NoteMainHidden = %d, want 1", win.noteHidden)
	}

	if !a.Quit() {
		t.Fatal("Quit should report success once started")
	}
	if win.quit != 1 {
		t.Fatalf("Quit = %d, want 1", win.quit)
	}
}

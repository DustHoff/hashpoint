//go:build windows

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"fyne.io/systray"
	"github.com/dusthoff/hashpoint/internal/app"
	"github.com/dusthoff/hashpoint/internal/crashguard"
	"github.com/dusthoff/hashpoint/internal/personio"
	"github.com/dusthoff/hashpoint/internal/storage"
)

// manualTagSlotCount caps how many tags we can show in the manual-tag
// submenu. We pre-allocate a fixed pool of slots and re-bind them as the tag
// list changes, rather than adding/removing items per refresh: it gives each
// slot one stable click-handler goroutine and avoids item/handler churn on
// every rescan. (fyne.io/systray does expose MenuItem.Remove(), but the pool
// is kept deliberately.) 64 is well above the realistic number of tags a
// single user keeps around.
const manualTagSlotCount = 64

// manualTagRefreshInterval controls how often the tray rescans the tag
// list. Tags are typically edited from the main UI, so a few seconds is
// imperceptible to the user yet keeps the menu fresh without wasting CPU.
const manualTagRefreshInterval = 3 * time.Second

// trayReadyTimeout bounds how long runTray waits for the native tray to come
// up before treating the start as a failed init. fyne/systray's Run blocks in
// GetMessage even when initInstance fails: registerSystray logs "unable to
// init instance" and returns, but Run still enters the message loop with no
// window, so it never returns and onTrayReady never fires. Without this
// timeout the collector would sit alive-but-tray-less forever, leaving the
// user a dead ghost icon that must be killed by hand. 8s is comfortably
// above both a healthy init (milliseconds) and the watchdog's 5s poll, so the
// short-lived retry is still observed alive between attempts and does not trip
// the watchdog's crash-loop cooldown.
const trayReadyTimeout = 8 * time.Second

func defaultSessionStore() personio.SessionStore {
	return personio.NewWinCredSessionStore()
}

func runTray(ctx context.Context, a *app.App, act trayActions, version string) {
	// systray.Run blocks until the tray's message loop ends. That happens on a
	// normal Quit (the ctx.Done path in onTrayReady) but also when the OS tears
	// the tray window down out from under us — most often as the machine enters
	// Modern Standby, which then hard-kills this process moments later (#21) —
	// and, critically, when the native init fails: fyne/systray then wedges in
	// GetMessage and Run never returns. We therefore run it on its own
	// goroutine and decide the outcome from a readiness signal, rather than
	// relying on Run returning.
	//
	// We deliberately do not try to rebuild the tray in-process: fyne/systray
	// keeps global state (quitOnce, the initialized window, the menu-item map)
	// that a second Run would not cleanly reset. Instead, an unexpected loop
	// exit (or a failed init) asks for a controlled restart — the watchdog
	// brings up a fresh collector, and thus a fresh, clickable icon, within
	// seconds. Safe() guards the native loop so even a panic there reaches the
	// outcome decision rather than silently ending the goroutine.
	ready := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		crashguard.Safe(slog.Default(), "tray-run", func() {
			systray.Run(func() { onTrayReady(ctx, a, act, version, ready) }, func() {
				slog.Info("tray: systray.Run returned")
			})
		})
	}()

	reason := waitTrayOutcome(ctx, ready, done, trayReadyTimeout)
	if reason == "" {
		return // expected: our own shutdown quit the tray
	}
	slog.Error("tray: requesting restart", "reason", reason)
	if act.trayLost != nil {
		act.trayLost()
	}
}

// waitTrayOutcome blocks until the tray's fate is decided and returns the
// reason a restart is needed, or "" when the exit is expected (our own
// shutdown). ready is closed by onTrayReady once the menu is up; done is closed
// when systray.Run returns. readyTimeout guards against a failed native init,
// which closes neither channel (Run wedges in GetMessage with no window, so
// onTrayReady never runs and Run never returns). It is split out from runTray
// so the decision logic is unit-testable without a live systray.
func waitTrayOutcome(ctx context.Context, ready, done <-chan struct{}, readyTimeout time.Duration) string {
	t := time.NewTimer(readyTimeout)
	defer t.Stop()

	// Phase 1 — wait for the tray to come up, fail to come up, or be cancelled.
	select {
	case <-ready:
		// Tray is up; fall through to phase 2.
	case <-done:
		// Run returned before signalling ready (e.g. a teardown racing
		// startup). The native loop will not come up on its own.
		if ctx.Err() != nil {
			return ""
		}
		return "tray loop ended before it became ready"
	case <-ctx.Done():
		// Our own shutdown beat the tray coming up. The native loop may be
		// wedged on a failed init, so do not block waiting for done.
		return ""
	case <-t.C:
		// Failed init: registerSystray logged the error and Run is now wedged
		// in GetMessage. Ask for a restart; trayLost hard-exits the process.
		if ctx.Err() != nil {
			return ""
		}
		return "tray did not become ready within timeout — native init likely failed"
	}

	// Phase 2 — tray is serving; wait for the loop to end or our shutdown.
	select {
	case <-done:
		if ctx.Err() != nil {
			return ""
		}
		return "tray message loop ended unexpectedly"
	case <-ctx.Done():
		<-done // tray is up, so Quit() returns promptly; let onExit finish
		return ""
	}
}

func onTrayReady(ctx context.Context, a *app.App, act trayActions, version string, ready chan struct{}) {
	systray.SetIcon(trayIcon())
	systray.SetTitle("Hashpoint")
	systray.SetTooltip("Hashpoint TimeTracker " + version)

	mOpen := systray.AddMenuItem("Öffnen", "Hauptfenster anzeigen")
	mPause := systray.AddMenuItemCheckbox("Pause Tracking", "Tracking pausieren", false)
	mSync := systray.AddMenuItem("Sync zu Personio (heute)", "Heutigen Tag synchronisieren")
	systray.AddSeparator()

	// Manual-tag submenu — clicking a tag closes any currently open manual
	// block and opens a new placeholder block under that tag from "now".
	// "Kein Tag" closes the active manual block. We pre-allocate a pool of
	// slots and rebind them as tags come and go (see refreshManualTagSlots)
	// rather than adding/removing items per refresh.
	mManualTag := systray.AddMenuItem("Manueller Tag", "Zeit manuell einem Tag zuordnen")
	mManualNone := mManualTag.AddSubMenuItem("Kein Tag (Stop)", "Manuelle Zuordnung beenden")
	go func() {
		for range mManualNone.ClickedCh {
			if err := a.StopManualTag(); err != nil {
				slog.Warn("tray: stop manual tag failed", "err", err)
			}
		}
	}()

	slots := newManualTagSlots(ctx, a, mManualTag)
	slots.refresh()
	go slots.runRefreshLoop(ctx)

	systray.AddSeparator()
	mAbout := systray.AddMenuItem(fmt.Sprintf("Über (%s)", version), "Versionsinfo")
	mHelp := systray.AddMenuItem("Hilfe", "Benutzerhandbuch öffnen")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Beenden", "App beenden")

	// Signal that the tray is fully built and clickable. This only runs once
	// the native init succeeded (systray invokes onReady from registerSystray
	// after initInstance), so it is the readiness gate runTray waits on.
	close(ready)

	for {
		select {
		case <-ctx.Done():
			systray.Quit()
			return
		case <-mOpen.ClickedCh:
			// HideWindowOnClose means the close button only hides the
			// window — the tray is the only path back without restarting.
			act.open()
		case <-mPause.ClickedCh:
			if a.IsTrackingPaused() {
				a.ResumeTracking()
				mPause.Uncheck()
			} else {
				a.PauseTracking()
				mPause.Check()
			}
		case <-mSync.ClickedCh:
			// Preflight first — if Personio already has periods today, the
			// modal is surfaced so the user can pick override vs import
			// instead of silently clobbering existing entries.
			a.RequestSyncToday()
		case <-mAbout.ClickedCh:
			slog.Info("about clicked", "version", version)
		case <-mHelp.ClickedCh:
			act.openHelp()
		case <-mQuit.ClickedCh:
			// act.quit drives the graceful path (monolith: Wails OnShutdown;
			// collector: cancel its context). It returns true only when no
			// graceful path took over, in which case we hard-stop the tray.
			if act.quit() {
				// Clean exit: drop the crash marker first, since os.Exit
				// skips the deferred Disarm.
				if act.disarm != nil {
					act.disarm()
				}
				systray.Quit()
				os.Exit(0)
			}
			return
		}
	}
}

// manualTagSlots backs the dynamic Manual-Tag submenu. We keep a fixed pool
// of slots and rewire them (Show/Hide/SetTitle) whenever the underlying tag
// list changes, instead of adding/removing items per refresh. Each slot has
// one click handler goroutine; the goroutine reads the slot's current tag id
// under mu, so updates are race-free.
type manualTagSlots struct {
	a      *app.App
	parent *systray.MenuItem

	mu    sync.RWMutex
	tagID []int64 // index → tag id, 0 means slot is hidden/unused
	last  []storage.Tag

	items []*systray.MenuItem
}

func newManualTagSlots(ctx context.Context, a *app.App, parent *systray.MenuItem) *manualTagSlots {
	s := &manualTagSlots{
		a:      a,
		parent: parent,
		tagID:  make([]int64, manualTagSlotCount),
		items:  make([]*systray.MenuItem, manualTagSlotCount),
	}
	for i := 0; i < manualTagSlotCount; i++ {
		item := parent.AddSubMenuItem("", "")
		item.Hide()
		s.items[i] = item
		idx := i
		go s.handleClicks(ctx, idx, item)
	}
	return s
}

func (s *manualTagSlots) handleClicks(ctx context.Context, idx int, item *systray.MenuItem) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-item.ClickedCh:
			s.mu.RLock()
			tagID := s.tagID[idx]
			s.mu.RUnlock()
			if tagID == 0 {
				continue
			}
			if err := s.a.StartManualTag(tagID, ""); err != nil {
				slog.Warn("tray: start manual tag failed", "tag_id", tagID, "err", err)
			}
		}
	}
}

func (s *manualTagSlots) runRefreshLoop(ctx context.Context) {
	t := time.NewTicker(manualTagRefreshInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.refresh()
		}
	}
}

// refresh pulls the current tag list and rebinds the slot pool. Slots
// that are no longer needed are hidden; reused slots only get SetTitle
// when the visible label actually changed, to avoid unnecessary native
// menu redraws.
func (s *manualTagSlots) refresh() {
	tags, err := s.a.ListTags()
	if err != nil {
		slog.Warn("tray: list tags for manual menu failed", "err", err)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if tagsEqual(s.last, tags) {
		return
	}

	ordered := groupTagsByParent(tags)

	visible := 0
	for _, t := range ordered {
		if t.Name == "" {
			continue
		}
		if visible >= len(s.items) {
			slog.Warn("tray: more tags than manual-tag slots — extras hidden",
				"slots", len(s.items), "tag_count", len(tags))
			break
		}
		label := manualTagLabel(t, ordered)
		item := s.items[visible]
		item.SetTitle(label)
		item.SetTooltip("Zeit dem Tag '" + label + "' zuordnen")
		item.Show()
		s.tagID[visible] = t.ID
		visible++
	}
	for i := visible; i < len(s.items); i++ {
		s.items[i].Hide()
		s.tagID[i] = 0
	}
	s.last = append(s.last[:0], tags...)
}

// groupTagsByParent returns tags ordered so each top-level tag is
// immediately followed by its own children. Orphaned sub-tags (parent
// missing) are appended at the end so they still appear in the menu.
func groupTagsByParent(tags []storage.Tag) []storage.Tag {
	byID := make(map[int64]storage.Tag, len(tags))
	for _, t := range tags {
		byID[t.ID] = t
	}
	childrenByParent := make(map[int64][]storage.Tag)
	var parents []storage.Tag
	for _, t := range tags {
		if t.ParentID == nil {
			parents = append(parents, t)
		} else {
			childrenByParent[*t.ParentID] = append(childrenByParent[*t.ParentID], t)
		}
	}
	out := make([]storage.Tag, 0, len(tags))
	for _, p := range parents {
		out = append(out, p)
		out = append(out, childrenByParent[p.ID]...)
	}
	for _, t := range tags {
		if t.ParentID != nil {
			if _, ok := byID[*t.ParentID]; !ok {
				out = append(out, t)
			}
		}
	}
	return out
}

// manualTagLabel renders a sub-tag as "Parent › Sub" so identically-named
// sub-tags under different parents stay distinguishable in the tray menu.
// Top-level tags render as just their name.
func manualTagLabel(t storage.Tag, all []storage.Tag) string {
	if t.ParentID == nil {
		return t.Name
	}
	for _, p := range all {
		if p.ID == *t.ParentID {
			return p.Name + " › " + t.Name
		}
	}
	return t.Name
}

// tagsEqual is a cheap equality check on the fields the manual-tag menu
// actually renders (id, name, parent). It intentionally ignores fields
// like Color or PersonioProjectID that don't affect the submenu.
func tagsEqual(a, b []storage.Tag) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID || a[i].Name != b[i].Name {
			return false
		}
		ap, bp := a[i].ParentID, b[i].ParentID
		if (ap == nil) != (bp == nil) {
			return false
		}
		if ap != nil && *ap != *bp {
			return false
		}
	}
	return true
}

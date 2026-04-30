//go:build windows

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/getlantern/systray"
	"github.com/onesi/hashpoint/internal/app"
	"github.com/onesi/hashpoint/internal/personio"
	"github.com/onesi/hashpoint/internal/storage"
	"github.com/onesi/hashpoint/internal/winapi"
)

// manualTagSlotCount caps how many tags we can show in the manual-tag
// submenu. systray has no API for removing menu items at runtime, so we
// pre-allocate a fixed pool of slots and re-bind them as the tag list
// changes. 64 is well above the realistic number of tags a single user
// keeps around.
const manualTagSlotCount = 64

// manualTagRefreshInterval controls how often the tray rescans the tag
// list. Tags are typically edited from the main UI, so a few seconds is
// imperceptible to the user yet keeps the menu fresh without wasting CPU.
const manualTagRefreshInterval = 3 * time.Second

func defaultSessionStore() personio.SessionStore {
	return personio.NewWinCredSessionStore()
}

func runTray(ctx context.Context, a *app.App, version string) {
	systray.Run(func() { onTrayReady(ctx, a, version) }, func() {})
}

func onTrayReady(ctx context.Context, a *app.App, version string) {
	systray.SetIcon(trayIcon())
	systray.SetTitle("Hashpoint")
	systray.SetTooltip("Hashpoint TimeTracker " + version)

	mOpen := systray.AddMenuItem("Öffnen", "Hauptfenster anzeigen")
	mPause := systray.AddMenuItemCheckbox("Pause Tracking", "Tracking pausieren", false)
	mSync := systray.AddMenuItem("Sync zu Personio (heute)", "Heutigen Tag synchronisieren")
	systray.AddSeparator()

	// Manual-tag submenu — clicking a tag closes any currently open manual
	// block and opens a new placeholder block under that tag from "now".
	// "Kein Tag" closes the active manual block. systray has no remove-item
	// API, so we pre-allocate a pool of slots and rebind them as tags come
	// and go (see refreshManualTagSlots).
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
	mAutostart := systray.AddMenuItemCheckbox("Autostart", "Mit Windows starten", false)
	mAbout := systray.AddMenuItem(fmt.Sprintf("Über (%s)", version), "Versionsinfo")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Beenden", "App beenden")

	autostart := winapi.NewAutostart("HashpointTimeTracker")
	if enabled, err := autostart.Enabled(); err == nil && enabled {
		mAutostart.Check()
	}

	for {
		select {
		case <-ctx.Done():
			systray.Quit()
			return
		case <-mOpen.ClickedCh:
			// HideWindowOnClose means the close button only hides the
			// window — the tray is the only path back without restarting.
			a.ShowWindow()
		case <-mPause.ClickedCh:
			if a.IsTrackingPaused() {
				a.ResumeTracking()
				mPause.Uncheck()
			} else {
				a.PauseTracking()
				mPause.Check()
			}
		case <-mSync.ClickedCh:
			today := time.Now().UTC().Format(time.RFC3339)
			if _, err := a.SyncDay(today); err != nil {
				slog.Warn("tray: sync failed", "err", err)
			}
		case <-mAutostart.ClickedCh:
			if mAutostart.Checked() {
				if err := autostart.Disable(); err == nil {
					mAutostart.Uncheck()
				}
			} else {
				exe, err := os.Executable()
				if err == nil {
					if err := autostart.Enable(exe); err == nil {
						mAutostart.Check()
					}
				}
			}
		case <-mAbout.ClickedCh:
			slog.Info("about clicked", "version", version)
		case <-mQuit.ClickedCh:
			// Route through Wails OnShutdown so today's tag blocks get
			// flushed and synced to Personio before the process exits.
			if !a.Quit() {
				systray.Quit()
				os.Exit(0)
			}
			return
		}
	}
}

// manualTagSlots backs the dynamic Manual-Tag submenu. systray exposes
// only Show/Hide/SetTitle on existing items — no removal — so we keep a
// fixed pool of slots and rewire them whenever the underlying tag list
// changes. Each slot has one click handler goroutine; the goroutine
// reads the slot's current tag id under mu, so updates are race-free.
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

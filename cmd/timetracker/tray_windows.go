//go:build windows

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/getlantern/systray"
	"github.com/onesi/hashpoint/internal/app"
	"github.com/onesi/hashpoint/internal/personio"
	"github.com/onesi/hashpoint/internal/winapi"
)

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
	// "Kein Tag" closes the active manual block. The tag list is snapshotted
	// at tray startup; tags added later require an app restart to appear.
	mManualTag := systray.AddMenuItem("Manueller Tag", "Zeit manuell einem Tag zuordnen")
	mManualNone := mManualTag.AddSubMenuItem("Kein Tag (Stop)", "Manuelle Zuordnung beenden")
	go func() {
		for range mManualNone.ClickedCh {
			if err := a.StopManualTag(); err != nil {
				slog.Warn("tray: stop manual tag failed", "err", err)
			}
		}
	}()
	if tags, err := a.ListTags(); err != nil {
		slog.Warn("tray: list tags for manual menu failed", "err", err)
	} else {
		for _, t := range tags {
			if t.Name == "" {
				continue
			}
			item := mManualTag.AddSubMenuItem(t.Name, "Zeit dem Tag '"+t.Name+"' zuordnen")
			tagID := t.ID
			go func() {
				for range item.ClickedCh {
					if err := a.StartManualTag(tagID); err != nil {
						slog.Warn("tray: start manual tag failed", "tag_id", tagID, "err", err)
					}
				}
			}()
		}
	}

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
			// Wails has no direct "show window" cross-cut from tray on plain
			// systray; opening is handled by Wails when the user clicks the
			// taskbar icon. As a fallback we log it.
			slog.Debug("tray: open clicked")
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
			systray.Quit()
			os.Exit(0)
		}
	}
}

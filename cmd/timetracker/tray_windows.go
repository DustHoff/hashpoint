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

func defaultCredStore() personio.CredentialStore {
	return personio.NewWinCredStore()
}

func runTray(ctx context.Context, a *app.App, version string) {
	systray.Run(func() { onTrayReady(ctx, a, version) }, func() {})
}

func onTrayReady(ctx context.Context, a *app.App, version string) {
	systray.SetTitle("Hashpoint")
	systray.SetTooltip("Hashpoint TimeTracker " + version)

	mOpen := systray.AddMenuItem("Öffnen", "Hauptfenster anzeigen")
	mPause := systray.AddMenuItemCheckbox("Pause Tracking", "Tracking pausieren", false)
	mSync := systray.AddMenuItem("Sync zu Personio (heute)", "Heutigen Tag synchronisieren")
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

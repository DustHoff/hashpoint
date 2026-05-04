// Command timetracker is the entry point for the Hashpoint TimeTracker app.
// It bootstraps storage, tracker, Personio session store, the Wails frontend
// and the system-tray icon, then waits for shutdown.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	hashpoint "github.com/onesi/hashpoint"
	"github.com/onesi/hashpoint/internal/app"
	"github.com/onesi/hashpoint/internal/config"
	"github.com/onesi/hashpoint/internal/logging"
	"github.com/onesi/hashpoint/internal/personio"
	"github.com/onesi/hashpoint/internal/storage"
	"github.com/onesi/hashpoint/internal/tagging"
	"github.com/onesi/hashpoint/internal/tracker"
	"github.com/onesi/hashpoint/internal/winapi"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	wails "github.com/wailsapp/wails/v2"
)

// version is overwritten via -ldflags in CI.
var (
	version   = "dev"
	commit    = "none"
	buildDate = "unknown"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	paths, err := config.ResolvePaths()
	if err != nil {
		return fmt.Errorf("resolve paths: %w", err)
	}
	if err := os.MkdirAll(paths.DataDir, 0o700); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	logLevel := slog.LevelInfo
	switch os.Getenv("HASHPOINT_LOG_LEVEL") {
	case "DEBUG", "debug":
		logLevel = slog.LevelDebug
	case "WARN", "warn":
		logLevel = slog.LevelWarn
	case "ERROR", "error":
		logLevel = slog.LevelError
	}
	logCloser, err := logging.Setup(logging.Options{
		Mode:    logging.ModeProd,
		Level:   logLevel,
		LogDir:  paths.LogDir,
		Console: false,
	})
	if err != nil {
		return fmt.Errorf("setup logging: %w", err)
	}
	defer func() {
		if logCloser != nil {
			_ = logCloser.Close()
		}
	}()

	cfg, err := config.Load(paths.ConfigFile)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	db, err := storage.Open(ctx, paths.DBFile)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer func() { _ = db.Close() }()

	tracks := storage.NewProcessTrackRepo(db)
	tagBlocks := storage.NewTagBlockRepo(db)
	tags := storage.NewTagRepo(db)
	rules := storage.NewRuleRepo(db)
	settings := storage.NewSettingsRepo(db)

	orchestrator := tagging.NewOrchestrator(tagBlocks, tracks, rules, slog.Default())
	orchestrator.SetGranularity(cfg.Tracking.TagBlockGranularity())

	// Tracker is reconfigurable: SaveConfig from the UI must adopt new poll
	// and idle-threshold values without a restart. The orchestrator listens
	// to focus events and owns the tag-block lifecycle.
	var trkMu sync.Mutex
	trk := tracker.New(tracker.Config{
		PollInterval:  cfg.Tracking.PollInterval(),
		IdleThreshold: cfg.Tracking.IdleThreshold(),
	}, tracks, slog.Default(), tracker.WithObserver(orchestrator))

	sessionStore := defaultSessionStore()

	syncerFor := func(sess *personio.Session) *personio.Syncer {
		if sess == nil {
			return nil
		}
		cli, err := personio.NewUIClient(personio.UIClientOptions{
			Session: sess,
			Logger:  slog.Default(),
		})
		if err != nil {
			slog.Warn("could not build personio client", "err", err)
			return nil
		}
		return personio.NewSyncer(cli, tagBlocks, tags, slog.Default())
	}

	hotkeyMgr := winapi.NewHotkeyManager(slog.Default())

	var a *app.App
	a = app.New(app.Deps{
		Tracks:       tracks,
		TagBlocks:    tagBlocks,
		Tags:         tags,
		Rules:        rules,
		Settings:     settings,
		Tracker:      trk,
		Orchestrator: orchestrator,
		Sessions:     sessionStore,
		SyncerFor:    syncerFor,
		ConfigPath:   paths.ConfigFile,
		Config:       cfg,
		OnConfigSet: func(c *config.Config) error {
			trkMu.Lock()
			defer trkMu.Unlock()
			*cfg = *c
			slog.Info("config updated",
				"poll_interval_sec", c.Tracking.PollIntervalSec,
				"idle_threshold_min", c.Tracking.IdleThresholdMin,
				"tag_block_granularity_min", c.Tracking.TagBlockGranularityMin,
				"tracking_enabled", c.Tracking.Enabled,
				"personio_tenant", c.Personio.Tenant,
				"autostart", c.UI.Autostart,
				"quick_tag_enabled", c.QuickTag.Enabled,
				"quick_tag_hotkey", c.QuickTag.Hotkey)
			if c.Tracking.Enabled {
				trk.Resume()
			} else {
				trk.Pause(ctx)
			}
			orchestrator.SetGranularity(c.Tracking.TagBlockGranularity())
			applyHotkey(hotkeyMgr, c.QuickTag, a, slog.Default())
			return nil
		},
		Version: app.VersionInfo{Version: version, Commit: commit, BuildDate: buildDate},
		Logger:  slog.Default(),
	})

	if err := hotkeyMgr.Start(); err != nil {
		slog.Warn("hotkey: manager start failed — quick-tag-picker disabled", "err", err)
	} else {
		applyHotkey(hotkeyMgr, cfg.QuickTag, a, slog.Default())
	}

	// Honour the persistent Enabled flag at startup so the user's last choice
	// in Settings survives across restarts.
	if !cfg.Tracking.Enabled {
		trk.Pause(ctx)
	}

	// Tracker goroutine.
	go func() {
		if err := trk.Run(ctx); err != nil && err != context.Canceled {
			slog.Error("tracker run failed", "err", err)
		}
	}()

	// OS signals → graceful shutdown via Wails so OnShutdown's flush-and-sync
	// runs. If Wails has not finished Startup yet, fall back to cancelling
	// the root context directly.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		if !a.Quit() {
			cancel()
		}
	}()

	// Tray runs on Windows only (no-op on other GOOS via build tag).
	go runTray(ctx, a, version)

	return wails.Run(&options.App{
		Title:            "Hashpoint TimeTracker",
		Width:            1200,
		Height:           800,
		MinWidth:         800,
		MinHeight:        600,
		WindowStartState: options.Maximised,
		AssetServer:      &assetserver.Options{Assets: hashpoint.Frontend},
		BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},
		OnStartup:        a.Startup,
		OnShutdown: func(c context.Context) {
			a.Shutdown(c)
			hotkeyMgr.Stop()
			flushAndSyncOnShutdown(trk, sessionStore, syncerFor, slog.Default())
			cancel()
		},
		HideWindowOnClose: true,
		OnBeforeClose:     a.OnWindowBeforeClose,
		Bind:              []any{a},
	})
}

// applyHotkey reconciles the configured quick-tag hotkey with the
// HotkeyManager. Called once at boot and on every SaveConfig — invalid
// strings are logged and the hotkey is left unregistered (the validator
// also rejects them, so reaching this with bad input means stale state).
func applyHotkey(mgr *winapi.HotkeyManager, qt config.QuickTagConfig, a *app.App, logger *slog.Logger) {
	if !qt.Enabled {
		if err := mgr.SetHotkey(false, 0, 0, nil); err != nil {
			logger.Warn("hotkey: disable failed", "err", err)
		}
		return
	}
	parsed, err := config.ParseHotkey(qt.Hotkey)
	if err != nil {
		logger.Warn("hotkey: parse failed — disabling", "hotkey", qt.Hotkey, "err", err)
		_ = mgr.SetHotkey(false, 0, 0, nil)
		return
	}
	if err := mgr.SetHotkey(true, parsed.Modifiers, parsed.VirtualKey, a.FireQuickTag); err != nil {
		logger.Warn("hotkey: register failed", "hotkey", parsed.Canonical, "err", err)
	}
}

// flushAndSyncOnShutdown closes any currently open process track and tag
// blocks via the tracker's Pause path, then pushes today's (local-day) tag
// blocks to Personio. Bounded by a hard timeout so shutdown never hangs.
// Skips silently when no Personio session is configured.
func flushAndSyncOnShutdown(
	trk *tracker.Tracker,
	sessions personio.SessionStore,
	syncerFor func(*personio.Session) *personio.Syncer,
	logger *slog.Logger,
) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if trk != nil {
		// Closes the open process track and triggers OnFocusCleared, which
		// in turn closes any open auto/manual tag block at a snapped time.
		trk.Pause(ctx)
	}

	if sessions == nil || syncerFor == nil {
		return
	}
	sess, err := sessions.Get()
	if err != nil || sess == nil {
		logger.Info("shutdown sync: no Personio session — skipping")
		return
	}
	syncer := syncerFor(sess)
	if syncer == nil {
		logger.Info("shutdown sync: syncer unavailable — skipping")
		return
	}

	now := time.Now()
	from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	to := from.Add(24 * time.Hour)
	res, err := syncer.SyncRange(ctx, from, to)
	if err != nil {
		logger.Warn("shutdown sync failed", "err", err)
		return
	}
	logger.Info("shutdown sync done",
		"periods", res.Periods,
		"blocks", res.BlocksProcessed,
		"skipped", res.BlocksSkipped,
		"errors", len(res.Errors))
}

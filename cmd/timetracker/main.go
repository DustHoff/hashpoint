// Command timetracker is the entry point for the Hashpoint TimeTracker app.
// It bootstraps storage, tracker, Personio session store, the Wails frontend
// and the system-tray icon, then waits for shutdown.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	hashpoint "github.com/dusthoff/hashpoint"
	"github.com/dusthoff/hashpoint/internal/app"
	"github.com/dusthoff/hashpoint/internal/config"
	"github.com/dusthoff/hashpoint/internal/entra"
	"github.com/dusthoff/hashpoint/internal/logging"
	"github.com/dusthoff/hashpoint/internal/personio"
	pluginhost "github.com/dusthoff/hashpoint/internal/plugin"
	"github.com/dusthoff/hashpoint/internal/storage"
	"github.com/dusthoff/hashpoint/internal/tagging"
	"github.com/dusthoff/hashpoint/internal/tracker"
	"github.com/dusthoff/hashpoint/internal/winapi"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	wails "github.com/wailsapp/wails/v2"
)

// singleInstanceMutexName is the session-local mutex used to enforce a
// single running Hashpoint process per user. The name is intentionally
// stable across versions so newer builds collide with older ones still
// running from the previous login.
const singleInstanceMutexName = "Hashpoint.SingleInstance"

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
	// Single-instance lock must come before file logging is configured:
	// without it a second instance would interleave entries into the same
	// timetracker.log and race the first instance on the SQLite DB and
	// the global Win32 hotkey. See issue #21 for the L262 case in the
	// production log where two instances briefly co-existed.
	lock, err := winapi.AcquireSingleInstanceLock(singleInstanceMutexName)
	if err != nil {
		if errors.Is(err, winapi.ErrAlreadyRunning) {
			// Stderr is hidden under -H windowsgui, so this is best-effort
			// for users launching from a console. The first instance keeps
			// running and stays visible in the tray.
			fmt.Fprintln(os.Stderr, "hashpoint: another instance is already running")
			return nil
		}
		return fmt.Errorf("acquire single-instance lock: %w", err)
	}
	defer func() { _ = lock.Release() }()

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

	// Seed bundled plugins from the install directory into the per-user
	// PluginsDir. The MSI drops plugin bundles under
	// <install-dir>\plugins-seed\<name>\; hashpoint runs as the interactive
	// user and can therefore reach the correct %APPDATA% to copy them in.
	// Seeding is best-effort — a failure must not block startup.
	if exe, err := os.Executable(); err == nil {
		seedDir := filepath.Join(filepath.Dir(exe), "plugins-seed")
		if err := pluginhost.Seed(seedDir, paths.PluginsDir, slog.Default()); err != nil {
			slog.Warn("plugin seed failed — continuing without seeded plugins", "err", err)
		}
	} else {
		slog.Warn("os.Executable failed — skipping plugin seed", "err", err)
	}

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
	oncallRepo := storage.NewOnCallRepo(db)
	pluginSettingsRepo := storage.NewPluginSettingsRepo(db, storage.NewDPAPICipher())

	orchestrator := tagging.NewOrchestrator(tagBlocks, tracks, rules, slog.Default())
	orchestrator.SetGranularity(cfg.Tracking.TagBlockGranularity())

	// Tracker is reconfigurable: SaveConfig from the UI must adopt new poll
	// and idle-threshold values without a restart. The orchestrator listens
	// to focus events and owns the tag-block lifecycle.
	var trkMu sync.Mutex
	trk := tracker.New(tracker.Config{
		PollInterval:               cfg.Tracking.PollInterval(),
		IdleThreshold:              cfg.Tracking.IdleThreshold(),
		CommunicationNames:         cfg.Communication.ProcessNames,
		CommunicationTitleExcludes: cfg.Communication.TitleExcludePhrases,
	}, tracks, slog.Default(), tracker.WithObserver(orchestrator))

	sessionStore := defaultSessionStore()

	// Entra ID is opt-in: build the manager lazily, only when client_id
	// and tenant_id are filled in. The closure is also wired into the
	// app so SaveConfig can rebuild the manager on every config change
	// without touching main.go again.
	entraFor := func(c config.EntraConfig) (entra.Manager, error) {
		if !c.Configured() {
			return nil, nil
		}
		return entra.NewManager(entra.Options{
			ClientID: c.ClientID,
			TenantID: c.TenantID,
			CacheDir: paths.AuthDir,
			Logger:   slog.Default(),
		})
	}

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
		Tracks:         tracks,
		TagBlocks:      tagBlocks,
		Tags:           tags,
		Rules:          rules,
		Settings:       settings,
		OnCall:         oncallRepo,
		Tracker:        trk,
		Orchestrator:   orchestrator,
		Sessions:       sessionStore,
		SyncerFor:      syncerFor,
		EntraFor:       entraFor,
		PluginsDir:     paths.PluginsDir,
		PluginSettings: pluginSettingsRepo,
		ConfigPath:     paths.ConfigFile,
		Config:         cfg,
		LogDir:         paths.LogDir,
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
				"quick_tag_enabled", c.QuickTag.Enabled,
				"quick_tag_hotkey", c.QuickTag.Hotkey,
				"communication_processes", c.Communication.ProcessNames,
				"communication_title_excludes_count", len(c.Communication.TitleExcludePhrases))
			if c.Tracking.Enabled {
				trk.Resume()
			} else {
				trk.Pause(ctx)
			}
			orchestrator.SetGranularity(c.Tracking.TagBlockGranularity())
			trk.SetCommunicationNames(c.Communication.ProcessNames)
			trk.SetCommunicationTitleExcludes(c.Communication.TitleExcludePhrases)
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
		if err := trk.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("tracker run failed", "err", err)
		}
	}()

	// Power monitor: pause the tracker on Modern Standby / sleep and
	// resume on wake. The Wails OnShutdown callback is not invoked when
	// Windows kills the WebView2 host during suspend (issue #21), so we
	// close open tracks at the suspend edge instead of letting recovery
	// fall back on the 5-minute idle heuristic. pausedBySuspend tracks
	// system-initiated pauses so we never accidentally un-pause a user
	// who paused tracking from the tray.
	var pausedBySuspend atomic.Bool
	power, powerErr := winapi.NewPowerMonitor(slog.Default(),
		func() {
			if trk.Paused() {
				slog.Debug("power: suspend — tracker already paused")
				return
			}
			slog.Info("power: suspend — pausing tracker")
			pausedBySuspend.Store(true)
			trk.Pause(ctx)
		},
		func() {
			if pausedBySuspend.Swap(false) {
				slog.Info("power: resume — resuming tracker")
				trk.Resume()
			} else {
				slog.Debug("power: resume — tracker was not suspend-paused")
			}
		},
	)
	if powerErr != nil {
		slog.Warn("power: monitor registration failed — suspend/resume edges will not be observed", "err", powerErr)
	} else {
		defer func() { _ = power.Close() }()
	}

	// OS signals → graceful shutdown via Wails so OnShutdown's flush runs.
	// If Wails has not finished Startup yet, fall back to cancelling the
	// root context directly.
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

	// onShutdownCompleted distinguishes a clean Wails shutdown (OnShutdown
	// ran) from an abnormal exit where Wails returns without invoking the
	// callback. The latter is the production symptom in issue #21:
	// WebView2 is killed during Modern Standby, wails.Run returns, no log
	// of shutdown, open tracks left behind. When that happens we run the
	// same cleanup OnShutdown would have run so DB state stays consistent.
	var onShutdownCompleted atomic.Bool
	runErr := wails.Run(&options.App{
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
			flushOnShutdown(trk, slog.Default())
			cancel()
			onShutdownCompleted.Store(true)
		},
		HideWindowOnClose: true,
		OnBeforeClose:     a.OnWindowBeforeClose,
		Bind:              []any{a},
	})

	if !onShutdownCompleted.Load() {
		slog.Warn("wails.Run returned without OnShutdown — running fallback cleanup",
			"err", runErr)
		a.Shutdown(context.Background())
		hotkeyMgr.Stop()
		flushOnShutdown(trk, slog.Default())
		cancel()
	} else if runErr != nil {
		slog.Warn("wails.Run returned an error after OnShutdown", "err", runErr)
	} else {
		slog.Info("wails.Run returned cleanly")
	}

	return runErr
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

// flushOnShutdown closes any currently open process track and tag blocks
// via the tracker's Pause path. Personio sync at shutdown was removed —
// system shutdowns kill the network before the request lands, so we sync
// the previous day on the next startup instead (see App.runStartupSync).
func flushOnShutdown(trk *tracker.Tracker, logger *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if trk != nil {
		// Closes the open process track and triggers OnFocusCleared, which
		// in turn closes any open auto/manual tag block at a snapped time.
		trk.Pause(ctx)
	}
	logger.Info("shutdown flush done")
}

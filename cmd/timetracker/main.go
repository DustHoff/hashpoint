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

	hashpoint "github.com/onesi/hashpoint"
	"github.com/onesi/hashpoint/internal/app"
	"github.com/onesi/hashpoint/internal/config"
	"github.com/onesi/hashpoint/internal/logging"
	"github.com/onesi/hashpoint/internal/personio"
	"github.com/onesi/hashpoint/internal/storage"
	"github.com/onesi/hashpoint/internal/tracker"
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

	blocks := storage.NewFocusBlockRepo(db)
	tags := storage.NewTagRepo(db)
	rules := storage.NewRuleRepo(db)
	settings := storage.NewSettingsRepo(db)

	// Tracker is reconfigurable: SaveConfig from the UI must adopt new poll
	// and idle-threshold values without a restart.
	var trkMu sync.Mutex
	trk := tracker.New(tracker.Config{
		PollInterval:  cfg.Tracking.PollInterval(),
		IdleThreshold: cfg.Tracking.IdleThreshold(),
	}, blocks, rules, slog.Default())

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
		return personio.NewSyncer(cli, blocks, tags, slog.Default())
	}

	a := app.New(app.Deps{
		Blocks:     blocks,
		Tags:       tags,
		Rules:      rules,
		Settings:   settings,
		Tracker:    trk,
		Sessions:   sessionStore,
		SyncerFor:  syncerFor,
		ConfigPath: paths.ConfigFile,
		Config:     cfg,
		OnConfigSet: func(c *config.Config) error {
			trkMu.Lock()
			defer trkMu.Unlock()
			// Tracker.Reconfigure is best-effort: at minimum we log; if the
			// implementation grows a hot-reload entry point we wire it here.
			slog.Info("config updated",
				"poll_interval_sec", c.Tracking.PollIntervalSec,
				"idle_threshold_min", c.Tracking.IdleThresholdMin,
				"personio_tenant", c.Personio.Tenant,
				"autostart", c.UI.Autostart)
			return nil
		},
		Version: app.VersionInfo{Version: version, Commit: commit, BuildDate: buildDate},
		Logger:  slog.Default(),
	})

	// Tracker goroutine.
	go func() {
		if err := trk.Run(ctx); err != nil && err != context.Canceled {
			slog.Error("tracker run failed", "err", err)
		}
	}()

	// OS signals → graceful shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	// Tray runs on Windows only (no-op on other GOOS via build tag).
	go runTray(ctx, a, version)

	return wails.Run(&options.App{
		Title:            "Hashpoint TimeTracker",
		Width:            1200,
		Height:           800,
		MinWidth:         800,
		MinHeight:        600,
		AssetServer:      &assetserver.Options{Assets: hashpoint.Frontend},
		BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},
		OnStartup:        a.Startup,
		OnShutdown: func(c context.Context) {
			a.Shutdown(c)
			cancel()
		},
		HideWindowOnClose: true,
		Bind:              []any{a},
	})
}

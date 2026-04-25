// Command timetracker is the entry point for the Hashpoint TimeTracker app.
// It bootstraps storage, tracker, Personio client, the Wails frontend and the
// system-tray icon, then waits for shutdown.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
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

	logCloser, err := logging.Setup(logging.Options{
		Mode:    logging.ModeProd,
		Level:   slog.LevelInfo,
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

	trk := tracker.New(tracker.Config{
		PollInterval:  cfg.Tracking.PollInterval(),
		IdleThreshold: cfg.Tracking.IdleThreshold(),
	}, blocks, rules, slog.Default())

	syncer := buildSyncer(cfg, blocks, tags)

	a := app.New(app.Deps{
		Blocks:   blocks,
		Tags:     tags,
		Rules:    rules,
		Settings: settings,
		Tracker:  trk,
		Syncer:   syncer,
		Version:  app.VersionInfo{Version: version, Commit: commit, BuildDate: buildDate},
		Logger:   slog.Default(),
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

func buildSyncer(cfg *config.Config, blocks storage.FocusBlockRepository, tags storage.TagRepository) *personio.Syncer {
	if cfg.Personio.ClientID == "" || cfg.Personio.EmployeeID == "" {
		return nil
	}
	store := defaultCredStore()
	client := personio.New(personio.Options{
		BaseURL:    cfg.Personio.BaseURL,
		ClientID:   cfg.Personio.ClientID,
		EmployeeID: cfg.Personio.EmployeeID,
		Store:      store,
		Logger:     slog.Default(),
	})
	return personio.NewSyncer(client, blocks, tags, slog.Default())
}

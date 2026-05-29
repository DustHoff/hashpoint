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
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	hashpoint "github.com/dusthoff/hashpoint"
	"github.com/dusthoff/hashpoint/internal/app"
	"github.com/dusthoff/hashpoint/internal/config"
	"github.com/dusthoff/hashpoint/internal/crashguard"
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
	os.Exit(appMain())
}

// appMain runs the app and returns the process exit code. Keeping os.Exit out
// of the same frame as the deferred RecoverFatal is deliberate: os.Exit would
// skip the defer, so the panic guard lives here (returning a code) while the
// single os.Exit stays in main. A panic that escapes every goroutine guard is
// logged with its stack and re-raised, so the process still exits non-zero.
func appMain() int {
	defer crashguard.RecoverFatal()
	if err := dispatch(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		return 1
	}
	return 0
}

// procMode selects which half of the application a process runs as.
type procMode int

const (
	// modeMonolith is the legacy single-process app (the shipped default).
	modeMonolith procMode = iota
	// modeCollector is the headless collector half of the split (ADR 0001).
	modeCollector
	// modeUI is the throwaway Wails shell the collector spawns.
	modeUI
)

// dispatch routes to the selected process mode. With no mode flag the legacy
// single-process app runs; --collector and --ui select the two halves of the
// collector/UI split (ADR 0001). The split modes are under construction and
// not yet the shipped default — run() remains the production path.
func dispatch(args []string) error {
	switch mode, pipe := parseArgs(args); mode {
	case modeCollector:
		return runCollector()
	case modeUI:
		return runUI(pipe)
	default:
		return run()
	}
}

// parseArgs does a minimal scan for the mode and pipe flags. It is deliberately
// tolerant of any other arguments the launcher or OS may append (e.g. on
// single-instance hand-off) rather than using flag.Parse, which would reject
// unknown flags.
func parseArgs(args []string) (procMode, string) {
	mode, pipe := modeMonolith, ""
	for _, a := range args {
		switch {
		case a == "--collector":
			mode = modeCollector
		case a == "--ui":
			mode = modeUI
		case strings.HasPrefix(a, "--pipe="):
			pipe = strings.TrimPrefix(a, "--pipe=")
		}
	}
	return mode, pipe
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

	paths, cfg, closeLog, err := bootstrap()
	if err != nil {
		return err
	}
	defer closeLog()

	// Crash sentinel: report a previous run that died without disarming, then
	// arm this run's marker. Disarm runs on every clean exit (including a panic
	// unwinding through this defer); an abrupt kill leaves the marker for the
	// next start to detect. Best-effort — an arm failure only disables crash
	// detection, it must not block startup.
	mk, err := crashguard.Start(ctx, paths.DataDir, crashguard.RoleMonolith,
		crashguard.Info{Version: version, Commit: commit}, slog.Default())
	if err != nil {
		slog.Warn("crashguard: arm failed — crash detection disabled", "err", err)
	}
	defer mk.Disarm()

	// nil sink ⇒ the app emits via the Wails runtime (the monolith default).
	// The monolith owns the in-process Wails window, so it drives it directly.
	d, err := buildDomain(ctx, paths, cfg, nil, newWindowedWindowController(slog.Default()))
	if err != nil {
		return err
	}
	defer func() { _ = d.dbClose() }()
	if d.power != nil {
		defer func() { _ = d.power.Close() }()
	}
	a := d.app

	// OS signals → graceful shutdown via Wails so OnShutdown's flush runs.
	// If Wails has not finished Startup yet, fall back to cancelling the
	// root context directly.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		defer crashguard.Recover(slog.Default(), "signal-handler")
		<-sigCh
		if !a.Quit() {
			cancel()
		}
	}()

	// Tray runs on Windows only (no-op on other GOOS via build tag). In the
	// monolith it drives the in-process Wails window directly.
	trayAct := trayActions{
		open:     a.ShowWindow,
		openHelp: a.OpenHelpTab,
		quit:     func() bool { return !a.Quit() },
		// The tray's hard-stop path (systray.Quit + os.Exit) bypasses the
		// deferred Disarm above, so it must remove the marker itself.
		disarm: mk.Disarm,
	}
	go func() {
		defer crashguard.Recover(slog.Default(), "tray")
		runTray(ctx, a, trayAct, version)
	}()

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
		AssetServer:      &assetserver.Options{Assets: hashpoint.Frontend, Middleware: cspMiddleware},
		BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},
		OnStartup:        a.Startup,
		OnShutdown: func(c context.Context) {
			d.shutdown(c)
			cancel()
			onShutdownCompleted.Store(true)
		},
		HideWindowOnClose: true,
		OnBeforeClose:     a.OnWindowBeforeClose,
		Bind:              []any{a},
	})

	switch {
	case !onShutdownCompleted.Load():
		slog.Warn("wails.Run returned without OnShutdown — running fallback cleanup",
			"err", runErr)
		d.shutdown(context.Background())
		cancel()
	case runErr != nil:
		slog.Warn("wails.Run returned an error after OnShutdown", "err", runErr)
	default:
		slog.Info("wails.Run returned cleanly")
	}

	return runErr
}

// bootstrap resolves paths, configures file logging, seeds bundled plugins and
// loads config. Shared by the monolith and the collector; returns a closer for
// the log writer. On error after the log is open, the closer is invoked before
// returning so the caller never has to.
func bootstrap() (config.Paths, *config.Config, func(), error) {
	paths, err := config.ResolvePaths()
	if err != nil {
		return config.Paths{}, nil, nil, fmt.Errorf("resolve paths: %w", err)
	}
	if err := os.MkdirAll(paths.DataDir, 0o700); err != nil {
		return config.Paths{}, nil, nil, fmt.Errorf("create data dir: %w", err)
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
		return config.Paths{}, nil, nil, fmt.Errorf("setup logging: %w", err)
	}
	closeLog := func() {
		if logCloser != nil {
			_ = logCloser.Close()
		}
	}

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
		closeLog()
		return config.Paths{}, nil, nil, fmt.Errorf("load config: %w", err)
	}
	return paths, cfg, closeLog, nil
}

// domainHandles bundles the long-lived backend that the collector owns and the
// monolith also runs in-process. Built by buildDomain.
type domainHandles struct {
	app     *app.App
	tracker *tracker.Tracker
	hotkey  *winapi.HotkeyManager
	power   *winapi.PowerMonitor // nil when registration failed
	dbClose func() error
}

// shutdown runs the clean-exit sequence: stop the plugin host + frontend
// bookkeeping, unregister the hotkey, and flush open tracks/blocks to the DB.
func (d *domainHandles) shutdown(ctx context.Context) {
	d.app.Shutdown(ctx)
	d.hotkey.Stop()
	flushOnShutdown(d.tracker, slog.Default())
}

// buildDomain wires storage, tracker, orchestrator, sessions, hotkey and the
// app facade from config — the shared backend for both the monolith (run) and
// the headless collector (runCollector). sink routes app events (nil ⇒ the
// Wails runtime in the monolith/UI; the collector passes an IPC-hub sink). It
// also honours the persisted tracking-enabled flag, starts the tracker
// goroutine and registers the suspend/resume power monitor; ctx governs their
// lifetime.
func buildDomain(ctx context.Context, paths config.Paths, cfg *config.Config, sink app.EventSink, window app.WindowController) (*domainHandles, error) {
	db, err := storage.Open(ctx, paths.DBFile)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

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
	// app so SaveConfig can rebuild the manager on every config change.
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
		Sink:           sink,
		Window:         window,
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
		defer crashguard.Recover(slog.Default(), "tracker-run")
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
			// Power callbacks run on an OS callback thread, so guard them: an
			// unrecovered panic here would abort the process from outside any
			// goroutine guard.
			crashguard.Safe(slog.Default(), "power-suspend", func() {
				if trk.Paused() {
					slog.Debug("power: suspend — tracker already paused")
					return
				}
				slog.Info("power: suspend — pausing tracker")
				pausedBySuspend.Store(true)
				trk.Pause(ctx)
			})
		},
		func() {
			crashguard.Safe(slog.Default(), "power-resume", func() {
				if pausedBySuspend.Swap(false) {
					slog.Info("power: resume — resuming tracker")
					trk.Resume()
				} else {
					slog.Debug("power: resume — tracker was not suspend-paused")
				}
			})
		},
	)
	if powerErr != nil {
		slog.Warn("power: monitor registration failed — suspend/resume edges will not be observed", "err", powerErr)
		power = nil
	}

	return &domainHandles{app: a, tracker: trk, hotkey: hotkeyMgr, power: power, dbClose: db.Close}, nil
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
	// The hotkey fires on the message-loop's own goroutine (winapi runs the
	// callback as `go cb()`), which has no crashguard around it. Wrap it so a
	// panic in the quick-tag handler is logged and contained instead of taking
	// the process down (issue #28).
	fire := func() { crashguard.Safe(logger, "hotkey-fire", a.FireQuickTag) }
	if err := mgr.SetHotkey(true, parsed.Modifiers, parsed.VirtualKey, fire); err != nil {
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

// Package app exposes Wails-bound methods to the frontend. The App struct
// is the single bridge between the JS layer and the Go backend; no other
// package speaks to Wails directly.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	hashpoint "github.com/dusthoff/hashpoint"
	"github.com/dusthoff/hashpoint/internal/config"
	"github.com/dusthoff/hashpoint/internal/entra"
	"github.com/dusthoff/hashpoint/internal/personio"
	pluginhost "github.com/dusthoff/hashpoint/internal/plugin"
	"github.com/dusthoff/hashpoint/internal/storage"
	"github.com/dusthoff/hashpoint/internal/tagging"
	"github.com/dusthoff/hashpoint/internal/tracker"
	"github.com/dusthoff/hashpoint/internal/winapi"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// Quick-tag-picker geometry (physical pixels). Intentionally compact —
// the popup is keyboard-driven and lists at most 10 entries.
const (
	quickTagPickerWidth  = 340
	quickTagPickerHeight = 420
	quickTagPickerMargin = 12
	quickTagRecentDays   = 30
	quickTagSlotCount    = 10
	quickTagOpenEvent    = "quick-tag-picker:open"
	quickTagCloseEvent   = "quick-tag-picker:close"
	helpOpenEvent        = "help:open"
	startupSyncEvent     = "startup-sync:result"
	// startupSyncConflictEvent fires when the startup-sync's preflight finds
	// existing periods on the day it was about to push. The frontend
	// listens and surfaces the same Override/Import modal as the manual
	// sync button.
	startupSyncConflictEvent = "startup-sync:conflict"

	// startupSyncTimeout caps the entire previous-day-sync run on startup.
	// Long enough for a slow Personio response, short enough that a stuck
	// goroutine cannot keep running for the rest of the session.
	startupSyncTimeout = 30 * time.Second
)

// StartupSyncStatus discriminates the payload variants emitted on the
// startupSyncEvent channel.
type StartupSyncStatus string

// Startup-sync status values. The trailing comment on each line is the
// authoritative description; revive's exported rule requires a comment
// at the block level so the const group is annotated here as well.
const (
	StartupSyncSkipped StartupSyncStatus = "skipped" // nothing to sync (no unsynced day)
	StartupSyncOK      StartupSyncStatus = "ok"      // sync ran, no day-level errors
	StartupSyncPartial StartupSyncStatus = "partial" // sync ran, some days failed
	StartupSyncFailed  StartupSyncStatus = "failed"  // hard failure (session expired, network, …)
)

// StartupSyncEvent is the JSON payload of startupSyncEvent. The frontend
// renders an info/success/error banner from this.
type StartupSyncEvent struct {
	Status          StartupSyncStatus `json:"status"`
	Day             string            `json:"day,omitempty"` // YYYY-MM-DD (local), empty for skipped
	Periods         int               `json:"periods,omitempty"`
	BlocksProcessed int               `json:"blocks_processed,omitempty"`
	BlocksSkipped   int               `json:"blocks_skipped,omitempty"`
	Errors          []string          `json:"errors,omitempty"`        // per-day messages from Result.Errors
	ErrorMessage    string            `json:"error_message,omitempty"` // hard error
}

// helpPageOrder controls the order of pages in the Help-tab sidebar. Slugs
// match file names in docs/user/ minus the .md extension. The list is the
// source of truth — any docs/user/*.md file not listed here is invisible
// to the in-app help (so adding a new page is a deliberate two-step:
// drop the file, append the slug here).
var helpPageOrder = []string{
	"README",
	"installation",
	"einstellungen",
	"zeiterfassung",
	"tags",
	"auto-tagging",
	"personio",
	"entra-id",
	"tray",
	"quick-tag",
}

// UserDocPage is the metadata payload used by the Help-tab sidebar.
type UserDocPage struct {
	Slug  string `json:"slug"`
	Title string `json:"title"`
}

// VersionInfo describes the running build.
type VersionInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
}

// Deps holds the wiring passed in from main.
type Deps struct {
	Tracks       storage.ProcessTrackRepository
	TagBlocks    storage.TagBlockRepository
	Tags         storage.TagRepository
	Rules        storage.RuleRepository
	Settings     storage.SettingsRepository
	OnCall       storage.OnCallRepository
	Tracker      *tracker.Tracker
	Orchestrator *tagging.Orchestrator
	Sessions     personio.SessionStore
	// PluginsDir is where Hashpoint scans for plugin binaries. Empty ⇒
	// the plugin system is disabled (no Host is constructed; oncall doc
	// submissions silently fail to "no plugin available", per product
	// decision: doc stays in draft).
	PluginsDir string
	// PluginSettings persists per-plugin config + the enable flag. Nil
	// disables the plugin system (same effect as empty PluginsDir).
	PluginSettings storage.PluginSettingsRepository
	// SyncerFor returns a Syncer wired against the given session, or nil if
	// the session is not usable (e.g. tenant unset). Constructed lazily so
	// session changes from the UI take effect immediately.
	SyncerFor func(*personio.Session) *personio.Syncer
	// EntraFor builds (or rebuilds) the Entra ID auth manager from the
	// given config. Returns (nil, nil) when the feature is not configured
	// — callers must treat that as "feature disabled" rather than as an
	// error. SaveConfig invokes this on ClientID/TenantID changes so a
	// freshly-typed pair takes effect without restart.
	EntraFor    func(config.EntraConfig) (entra.Manager, error)
	ConfigPath  string
	Config      *config.Config
	OnConfigSet func(*config.Config) error
	Version     VersionInfo
	Logger      *slog.Logger
}

// App is the Wails-bound facade. Methods on *App must be safe to call from
// the JS layer concurrently.
type App struct {
	ctx    context.Context
	deps   Deps
	logger *slog.Logger

	mu            sync.Mutex
	cfg           *config.Config
	started       bool
	windowVisible bool
	// entraMgr is the live Entra ID auth manager, or nil while the
	// feature is dormant. SaveConfig swaps it under entraMu when the
	// user updates client_id / tenant_id; readers must take the lock.
	entraMu  sync.Mutex
	entraMgr entra.Manager

	quickTagState quickTagWindowState

	// pluginHost is the live plugin manager — constructed in New() iff
	// deps.PluginsDir is non-empty. nil otherwise so the OnCall* methods
	// can short-circuit cleanly when the feature is disabled.
	pluginHost *pluginhost.Host
	// personioSrc serves HostAPI.RequestPersonioSession for plugins.
	// Constructed iff a Personio session store is configured; nil
	// otherwise. The source owns its own mutex — no App-level lock is
	// taken across the (potentially minute-long) CDP reauth.
	personioSrc *personioSessionSource
	// validatePersonio is the function used by PersonioCheck to probe
	// the stored cookies. Defaults to personio.Validate in production;
	// overridable from tests so the probe doesn't have to hit
	// app.personio.com.
	validatePersonio func(ctx context.Context, sess *personio.Session) error
}

// quickTagWindowState captures the main-window placement before the
// quick-tag-picker took over. Restored on dismiss so the user's normal
// layout returns intact.
type quickTagWindowState struct {
	saved      bool
	wasVisible bool
	width      int
	height     int
	x          int
	y          int
}

// New constructs the app from its dependencies. If the bundled config
// already has client_id/tenant_id filled in (i.e. the user configured
// Entra ID in a previous session), the manager is built up-front so the
// status badge has data on first render. A build failure is logged and
// shrugged off — the rest of the app stays fully functional.
func New(deps Deps) *App {
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	a := &App{
		deps:             deps,
		logger:           deps.Logger,
		cfg:              deps.Config,
		ctx:              context.Background(),
		windowVisible:    true,
		validatePersonio: personio.Validate,
	}
	if deps.EntraFor != nil && deps.Config != nil && deps.Config.Entra.Configured() {
		mgr, err := deps.EntraFor(deps.Config.Entra)
		if err != nil {
			a.logger.Warn("entra: initial manager construction failed — feature dormant",
				"err", err)
		} else {
			a.entraMgr = mgr
		}
	}

	// Build the Personio session source up-front so the plugin host's
	// PersonioSource callback can return it on demand. Only meaningful
	// when a session store is wired; without one the callback always
	// returns nil and plugins see sdk.ErrPersonioNotAvailable.
	if deps.Sessions != nil {
		a.personioSrc = newPersonioSessionSource(deps.Sessions, a.currentPersonioTenant, a.logger)
	}

	// Construct the plugin host if a plugins directory + settings
	// repo are provided. The host reads/writes per-plugin config
	// directly through the repo; the App layer never has to shuttle
	// values through config.toml. Startup() launches plugins in a
	// goroutine.
	if deps.PluginsDir != "" && deps.PluginSettings != nil {
		a.pluginHost = pluginhost.NewHost(pluginhost.HostDeps{
			Logger:     a.logger,
			PluginsDir: deps.PluginsDir,
			Settings:   deps.PluginSettings,
			OnDiscovered: func(info pluginhost.Info) {
				// a.ctx is set in Startup(); the discovery loop only runs
				// after Start() (which is invoked from Startup), so by
				// the time this fires the context is guaranteed valid.
				if a.ctx == nil {
					return
				}
				wailsruntime.EventsEmit(a.ctx, PluginDiscoveredEvent, info)
			},
			OnStateChanged: func(info pluginhost.Info) {
				// Same a.ctx caveat as OnDiscovered: the watcher only
				// spawns from a successful launch which can only happen
				// after Start(), so a.ctx is non-nil by the time this
				// fires.
				if a.ctx == nil {
					return
				}
				wailsruntime.EventsEmit(a.ctx, PluginStateChangedEvent, info)
			},
			// Hand running plugins access to the current Entra ID
			// manager via the host's bound HostAPI. Re-evaluated on
			// every plugin call so SaveConfig swapping a.entraMgr
			// takes effect without a plugin reload. Returns nil while
			// the feature is dormant — the bound API then surfaces
			// sdk.ErrEntraNotAvailable to the calling plugin.
			EntraSource: a.currentEntraTokenSource,
			// Hand running plugins access to the host's Personio
			// session (cookies + CSRF) via the bound HostAPI. The
			// source owns the mutex that serialises concurrent
			// re-auth flows. Returns nil while no tenant is
			// configured — plugins then see sdk.ErrPersonioNotAvailable.
			PersonioSource: a.currentPersonioSessionSource,
		})
	}

	// Wire the orchestrator's block-closed hook to the oncall Recheck
	// pipeline. The hook is best-effort — failures are logged, not
	// surfaced — because tagging operations must not be blocked by
	// downstream plugin bookkeeping.
	if deps.Orchestrator != nil {
		deps.Orchestrator.SetBlockClosedHook(a.onBlockClosedForOnCall)
	}

	// Wire the orchestrator's plugin auto-tag fallback. Every running
	// ProcessAutoTagHandler may participate; the adapter materialises
	// the plugin-supplied tag-name path against the tags table on
	// demand. Skipped when no plugin host or tag repo is wired — the
	// orchestrator's Resolve calls then just no-op back to nil.
	if a.pluginHost != nil && deps.Orchestrator != nil && deps.Tags != nil {
		adapter := newPluginAutoTagAdapter(a.pluginHost, deps.Tags, a.logger)
		deps.Orchestrator.SetPluginResolver(adapter)
	}
	return a
}

// Startup is invoked by Wails once the runtime is ready. We piggy-back on
// it to close any dangling open-ended manual tag block left over from a
// previous run — the orchestrator computes the correct close time using
// the last process-track end (or `now` when tracking is disabled) — and to
// kick off the previous-day Personio sync.
func (a *App) Startup(ctx context.Context) {
	a.mu.Lock()
	a.ctx = ctx
	a.started = true
	a.windowVisible = true
	cfg := a.cfg
	a.mu.Unlock()
	a.logger.Info("frontend started")

	if a.deps.Orchestrator != nil {
		fallback := time.Now().UTC()
		if err := a.deps.Orchestrator.CloseDanglingManualAtStartup(ctx, fallback); err != nil {
			a.logger.Warn("startup: close dangling manual failed", "err", err)
		}
		if err := a.deps.Orchestrator.Recover(ctx); err != nil {
			a.logger.Warn("startup: orchestrator recover failed", "err", err)
		}
	}

	// Launch installed plugins in a detached goroutine. Each plugin
	// starts its own subprocess + handshake, which can take a noticeable
	// fraction of a second; we deliberately don't block the main window
	// behind it. Failures are recorded on the pluginhost.Info for the
	// settings UI but never propagate up.
	if a.pluginHost != nil {
		go func() {
			if err := a.pluginHost.Start(ctx); err != nil {
				a.logger.Warn("plugin host start failed", "err", err)
			}
		}()
	}

	// Background goroutine: previous-day Personio sync. Runs detached so
	// the main window appears without waiting on the network. Result is
	// reported to the frontend via startupSyncEvent (banner).
	go a.runStartupSync(ctx)

	_ = cfg
}

// runStartupSync looks for the most recent local day before today that
// still has unsynced tag blocks and pushes it to Personio. The result
// (ok / partial / failed / skipped) is emitted on startupSyncEvent so the
// frontend can render a banner. Skips silently — no banner — when no
// Personio session is configured.
func (a *App) runStartupSync(parent context.Context) {
	if a.deps.TagBlocks == nil || a.deps.Sessions == nil || a.deps.SyncerFor == nil {
		return
	}
	sess, err := a.deps.Sessions.Get()
	if err != nil || sess == nil {
		a.logger.Info("startup sync: no Personio session — skipping silently")
		return
	}
	syncer := a.deps.SyncerFor(sess)
	if syncer == nil {
		a.logger.Info("startup sync: syncer unavailable — skipping silently")
		return
	}

	ctx, cancel := context.WithTimeout(parent, startupSyncTimeout)
	defer cancel()

	loc := time.Local
	now := time.Now().In(loc)
	cutoff := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)

	day, ok, err := a.deps.TagBlocks.LatestUnsyncedDayBefore(ctx, cutoff, loc)
	if err != nil {
		a.logger.Warn("startup sync: lookup failed", "err", err)
		a.emitStartupSync(StartupSyncEvent{
			Status:       StartupSyncFailed,
			ErrorMessage: err.Error(),
		})
		return
	}
	if !ok {
		a.logger.Info("startup sync: nothing to do — all prior days fully synced")
		return
	}

	dayStr := day.Format("2006-01-02")
	a.logger.Info("startup sync: running", "day", dayStr)

	// Preflight first: if Personio already has work-type periods on the
	// day, surface the Override/Import modal instead of clobbering them.
	pre, err := syncer.Preflight(ctx, day)
	if err != nil {
		a.logger.Warn("startup sync: preflight failed", "day", dayStr, "err", err)
		a.emitStartupSync(StartupSyncEvent{
			Status:       StartupSyncFailed,
			Day:          dayStr,
			ErrorMessage: err.Error(),
		})
		return
	}
	if pre.HasExistingPeriods() {
		a.logger.Info("startup sync: existing periods detected — handing off to user",
			"day", dayStr, "existing", len(pre.ExistingPeriods))
		a.emitStartupConflict(pre)
		return
	}

	res, err := syncer.SyncRange(ctx, day, day.Add(24*time.Hour))
	if err != nil {
		a.logger.Warn("startup sync: failed", "day", dayStr, "err", err)
		a.emitStartupSync(StartupSyncEvent{
			Status:       StartupSyncFailed,
			Day:          dayStr,
			ErrorMessage: err.Error(),
		})
		return
	}

	ev := StartupSyncEvent{
		Day:             dayStr,
		Periods:         res.Periods,
		BlocksProcessed: res.BlocksProcessed,
		BlocksSkipped:   res.BlocksSkipped,
		Errors:          res.Errors,
	}
	switch {
	case len(res.Errors) > 0:
		ev.Status = StartupSyncPartial
	case res.Periods == 0 && res.BlocksProcessed == 0:
		// Day had unsynced rows but they were all "should-skip" (deleted tag,
		// sync_to_personio=off, …). Don't pester the user with a banner.
		a.logger.Info("startup sync: nothing pushed — all blocks skipped",
			"day", dayStr, "skipped", res.BlocksSkipped)
		return
	default:
		ev.Status = StartupSyncOK
	}
	a.logger.Info("startup sync: done",
		"day", dayStr,
		"status", ev.Status,
		"periods", ev.Periods,
		"blocks", ev.BlocksProcessed,
		"skipped", ev.BlocksSkipped,
		"errors", len(ev.Errors))
	a.emitStartupSync(ev)
}

func (a *App) emitStartupSync(ev StartupSyncEvent) {
	a.mu.Lock()
	ctx, ready := a.ctx, a.started
	a.mu.Unlock()
	if !ready || ctx == nil {
		return
	}
	wailsruntime.EventsEmit(ctx, startupSyncEvent, ev)
}

func (a *App) emitStartupConflict(pre *personio.SyncPreflight) {
	a.mu.Lock()
	ctx, ready := a.ctx, a.started
	a.mu.Unlock()
	if !ready || ctx == nil {
		return
	}
	wailsruntime.EventsEmit(ctx, startupSyncConflictEvent, pre)
}

// Shutdown is invoked by Wails on window close. Tracker shutdown is handled
// in main; nothing to do here.
func (a *App) Shutdown(ctx context.Context) {
	if a.pluginHost != nil {
		if err := a.pluginHost.Stop(ctx); err != nil {
			a.logger.Warn("plugin host stop failed", "err", err)
		}
	}
}

// ShowWindow brings the Wails main window to the foreground.
func (a *App) ShowWindow() {
	a.mu.Lock()
	ctx, ready := a.ctx, a.started
	a.windowVisible = true
	a.mu.Unlock()
	if !ready || ctx == nil {
		a.logger.Warn("app: ShowWindow called before Wails Startup — ignoring")
		return
	}
	wailsruntime.WindowShow(ctx)
	wailsruntime.WindowUnminimise(ctx)
}

// OpenHelpTab brings the main window forward and tells the frontend to
// switch to the Hilfe tab. Wired into the tray "Hilfe" item.
func (a *App) OpenHelpTab() {
	a.ShowWindow()
	a.mu.Lock()
	ctx, ready := a.ctx, a.started
	a.mu.Unlock()
	if !ready || ctx == nil {
		a.logger.Warn("OpenHelpTab: window not ready — event dropped")
		return
	}
	wailsruntime.EventsEmit(ctx, helpOpenEvent)
}

// ListUserDocs returns the embedded user-manual pages in sidebar order.
// Each entry's title is the first H1 in the markdown (so renaming a page
// is a one-line change inside the .md file, not a backend update).
func (a *App) ListUserDocs() ([]UserDocPage, error) {
	out := make([]UserDocPage, 0, len(helpPageOrder))
	for _, slug := range helpPageOrder {
		b, err := hashpoint.UserDocs.ReadFile("docs/user/" + slug + ".md")
		if err != nil {
			a.logger.Warn("help: missing doc page", "slug", slug, "err", err)
			continue
		}
		out = append(out, UserDocPage{Slug: slug, Title: extractDocTitle(string(b), slug)})
	}
	return out, nil
}

// GetUserDoc returns the raw markdown for a single page. Slugs are
// validated against helpPageOrder so this method cannot be coaxed into
// reading arbitrary files from the embed FS.
func (a *App) GetUserDoc(slug string) (string, error) {
	if !isAllowedDocSlug(slug) {
		return "", fmt.Errorf("unknown doc slug: %s", slug)
	}
	b, err := hashpoint.UserDocs.ReadFile("docs/user/" + slug + ".md")
	if err != nil {
		return "", fmt.Errorf("read user doc %s: %w", slug, err)
	}
	return string(b), nil
}

func isAllowedDocSlug(slug string) bool {
	for _, s := range helpPageOrder {
		if s == slug {
			return true
		}
	}
	return false
}

// extractDocTitle returns the first markdown H1 ("# ...") in the page,
// trimmed. Falls back to the slug when no heading is found.
func extractDocTitle(md, fallback string) string {
	for _, line := range strings.Split(md, "\n") {
		s := strings.TrimSpace(line)
		if strings.HasPrefix(s, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(s, "#"))
		}
		if s != "" {
			break
		}
	}
	return fallback
}

// OnWindowBeforeClose is wired into Wails' OnBeforeClose hook. With
// HideWindowOnClose=true the window is hidden (not closed) when the user
// clicks X — we use this hook to track that transition so the
// quick-tag-picker dismiss can hide vs. restore correctly.
//
// Returns false (do not prevent close) so Wails proceeds with its normal
// hide-on-close behaviour.
func (a *App) OnWindowBeforeClose(_ context.Context) bool {
	a.mu.Lock()
	a.windowVisible = false
	a.mu.Unlock()
	return false
}

// Quit triggers a graceful Wails shutdown so OnShutdown runs (closing open
// blocks). Returns false when Wails has not finished Startup yet — in that
// case the caller must fall back to cancelling the root context directly.
func (a *App) Quit() bool {
	a.mu.Lock()
	ctx, ready := a.ctx, a.started
	a.mu.Unlock()
	if !ready || ctx == nil {
		return false
	}
	wailsruntime.Quit(ctx)
	return true
}

// Version returns build metadata.
func (a *App) Version() VersionInfo { return a.deps.Version }

// LogFrontend ships a log record from the React layer into the same slog
// pipeline as the rest of the app.
func (a *App) LogFrontend(level, message string, fields map[string]any) {
	logger := a.logger
	if logger == nil {
		logger = slog.Default()
	}
	attrs := make([]any, 0, len(fields)*2+2)
	attrs = append(attrs, "src", "frontend")
	for k, v := range fields {
		attrs = append(attrs, k, v)
	}
	switch strings.ToLower(level) {
	case "error":
		logger.Error(message, attrs...)
	case "warn", "warning":
		logger.Warn(message, attrs...)
	case "debug":
		logger.Debug(message, attrs...)
	default:
		logger.Info(message, attrs...)
	}
}

// ----- Process tracks -----------------------------------------------------

// ProcessTracksByDay returns every raw process track on the given UTC day.
func (a *App) ProcessTracksByDay(dayRFC3339 string) ([]storage.ProcessTrack, error) {
	day, err := time.Parse(time.RFC3339, dayRFC3339)
	if err != nil {
		return nil, fmt.Errorf("parse day: %w", err)
	}
	return a.deps.Tracks.ListByDay(a.ctx, day.UTC())
}

// ProcessTracksBetween returns process tracks in [from, to).
func (a *App) ProcessTracksBetween(fromRFC3339, toRFC3339 string) ([]storage.ProcessTrack, error) {
	from, err := time.Parse(time.RFC3339, fromRFC3339)
	if err != nil {
		return nil, fmt.Errorf("parse from: %w", err)
	}
	to, err := time.Parse(time.RFC3339, toRFC3339)
	if err != nil {
		return nil, fmt.Errorf("parse to: %w", err)
	}
	return a.deps.Tracks.ListBetween(a.ctx, from.UTC(), to.UTC())
}

// ----- Tag blocks ---------------------------------------------------------

// TagBlocksByDay returns every tag block on the given UTC day.
func (a *App) TagBlocksByDay(dayRFC3339 string) ([]storage.TagBlock, error) {
	day, err := time.Parse(time.RFC3339, dayRFC3339)
	if err != nil {
		return nil, fmt.Errorf("parse day: %w", err)
	}
	return a.deps.TagBlocks.ListByDay(a.ctx, day.UTC())
}

// TagBlocksBetween returns tag blocks in [from, to).
func (a *App) TagBlocksBetween(fromRFC3339, toRFC3339 string) ([]storage.TagBlock, error) {
	from, err := time.Parse(time.RFC3339, fromRFC3339)
	if err != nil {
		return nil, fmt.Errorf("parse from: %w", err)
	}
	to, err := time.Parse(time.RFC3339, toRFC3339)
	if err != nil {
		return nil, fmt.Errorf("parse to: %w", err)
	}
	return a.deps.TagBlocks.ListBetween(a.ctx, from.UTC(), to.UTC())
}

// CreateManualTagRange tags the given time range manually. Snaps to
// granularity. Trims/splits/deletes auto-tag blocks the range overlaps;
// rejects overlap with existing manual blocks. After the orchestrator
// commits, recheckOnCallOverlapping reconciles any blocks the range
// touched so a freshly-created off-hours block enqueues its doc.
func (a *App) CreateManualTagRange(startRFC3339, endRFC3339 string, tagID int64, description string) error {
	start, err := time.Parse(time.RFC3339, startRFC3339)
	if err != nil {
		return fmt.Errorf("parse start: %w", err)
	}
	end, err := time.Parse(time.RFC3339, endRFC3339)
	if err != nil {
		return fmt.Errorf("parse end: %w", err)
	}
	if a.deps.Orchestrator == nil {
		return errors.New("orchestrator not configured")
	}
	if err := a.deps.Orchestrator.CreateManualRange(a.ctx, tagID, description, start.UTC(), end.UTC()); err != nil {
		return err
	}
	a.recheckOnCallOverlapping(a.ctx, start.UTC(), end.UTC())
	return nil
}

// ResizeTagBlock changes the start and end of a closed tag block. The new
// range snaps to granularity and is rejected if it would overlap another
// tag block. Auto-tag blocks are promoted to manual on resize. After a
// successful resize the block's OnCall doc may need to be marked stale
// (block moved fully into working hours) or freshly enqueued (block
// moved into off-hours); recheckOnCallByID handles both.
func (a *App) ResizeTagBlock(id int64, startRFC3339, endRFC3339 string) error {
	a.logger.Info("app: ResizeTagBlock", "id", id, "start", startRFC3339, "end", endRFC3339)
	start, err := time.Parse(time.RFC3339, startRFC3339)
	if err != nil {
		return fmt.Errorf("parse start: %w", err)
	}
	end, err := time.Parse(time.RFC3339, endRFC3339)
	if err != nil {
		return fmt.Errorf("parse end: %w", err)
	}
	if a.deps.Orchestrator == nil {
		return errors.New("orchestrator not configured")
	}
	if err := a.deps.Orchestrator.ResizeBlock(a.ctx, id, start.UTC(), end.UTC()); err != nil {
		return err
	}
	a.recheckOnCallByID(a.ctx, id)
	return nil
}

// SetTagBlockDescription updates the description on a tag block.
func (a *App) SetTagBlockDescription(id int64, description string) error {
	d := strings.TrimSpace(description)
	var ptr *string
	if d != "" {
		ptr = &d
	}
	return a.deps.TagBlocks.SetDescription(a.ctx, id, ptr)
}

// SetTagBlockTag re-points an existing tag block to a different tag. The
// block's OnCall doc may transition stale↔active if the new tag is in/out
// of the configured on-call set; recheckOnCallByID handles both.
func (a *App) SetTagBlockTag(id, tagID int64) error {
	if tagID <= 0 {
		return fmt.Errorf("invalid tag id: %d", tagID)
	}
	if err := a.deps.TagBlocks.SetTag(a.ctx, id, tagID); err != nil {
		return err
	}
	a.recheckOnCallByID(a.ctx, id)
	return nil
}

// DeleteTagBlock removes a tag block.
func (a *App) DeleteTagBlock(id int64) error {
	a.logger.Info("app: DeleteTagBlock", "id", id)
	return a.deps.TagBlocks.Delete(a.ctx, id)
}

// DeleteTagBlocks removes a batch of tag blocks. Returns the count actually
// deleted.
func (a *App) DeleteTagBlocks(ids []int64) (int, error) {
	a.logger.Info("app: DeleteTagBlocks requested", "count", len(ids))
	if len(ids) == 0 {
		return 0, nil
	}
	deleted := 0
	for _, id := range ids {
		if err := a.deps.TagBlocks.Delete(a.ctx, id); err != nil {
			return deleted, fmt.Errorf("delete tag block %d: %w", id, err)
		}
		deleted++
	}
	return deleted, nil
}

// ----- Tags ---------------------------------------------------------------

// ListTags returns all tags.
func (a *App) ListTags() ([]storage.Tag, error) { return a.deps.Tags.List(a.ctx) }

// CreateTag normalizes the name and inserts the tag.
func (a *App) CreateTag(t storage.Tag) (*storage.Tag, error) {
	name, err := tagging.NormalizeName(t.Name)
	if err != nil {
		return nil, err
	}
	t.Name = name
	if err := a.deps.Tags.Create(a.ctx, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// UpdateTag persists changes to a tag.
func (a *App) UpdateTag(t storage.Tag) error {
	name, err := tagging.NormalizeName(t.Name)
	if err != nil {
		return err
	}
	t.Name = name
	return a.deps.Tags.Update(a.ctx, &t)
}

// DeleteTag removes a tag and its sub-tags (FK cascade).
//
// After a successful delete we also reconcile OnCall.TagIDs: any ID in the
// config that no longer corresponds to an existing tag is dropped. This
// covers the case where the user removed a configured on-call root (or
// any of its descendants) and would otherwise leave stale IDs behind that
// silently disable the feature for those branches.
func (a *App) DeleteTag(id int64) error {
	if err := a.deps.Tags.Delete(a.ctx, id); err != nil {
		return err
	}
	if err := a.pruneOnCallTagIDs(a.ctx); err != nil {
		// Pruning failure is non-fatal — the delete succeeded; we log and
		// let the next SaveConfig re-validate.
		a.logger.Warn("DeleteTag: oncall tag-id reconciliation failed", "err", err)
	}
	return nil
}

// pruneOnCallTagIDs drops any OnCall.TagIDs entry whose tag no longer
// exists. No-op when the resulting list is identical to the current one.
// Safe to call from any code path that mutates the tag set.
func (a *App) pruneOnCallTagIDs(ctx context.Context) error {
	a.mu.Lock()
	if a.cfg == nil {
		a.mu.Unlock()
		return nil
	}
	current := append([]int64(nil), a.cfg.OnCall.TagIDs...)
	a.mu.Unlock()
	if len(current) == 0 {
		return nil
	}
	tags, err := a.deps.Tags.List(ctx)
	if err != nil {
		return fmt.Errorf("list tags: %w", err)
	}
	alive := make(map[int64]struct{}, len(tags))
	for _, t := range tags {
		alive[t.ID] = struct{}{}
	}
	filtered := make([]int64, 0, len(current))
	for _, id := range current {
		if _, ok := alive[id]; ok {
			filtered = append(filtered, id)
		}
	}
	if len(filtered) == len(current) {
		return nil
	}
	a.mu.Lock()
	if a.cfg == nil {
		a.mu.Unlock()
		return nil
	}
	next := *a.cfg
	next.OnCall.TagIDs = filtered
	a.cfg = &next
	a.mu.Unlock()
	if a.deps.ConfigPath != "" {
		if err := config.Save(a.deps.ConfigPath, &next); err != nil {
			return fmt.Errorf("persist pruned config: %w", err)
		}
	}
	a.logger.Info("oncall: pruned stale tag ids from config",
		"removed", len(current)-len(filtered), "remaining", len(filtered))
	return nil
}

// ----- Rules --------------------------------------------------------------

// ListRules returns all auto-tag rules.
func (a *App) ListRules() ([]storage.Rule, error) { return a.deps.Rules.List(a.ctx) }

// CreateRule validates the pattern and inserts a rule.
func (a *App) CreateRule(r storage.Rule) (*storage.Rule, error) {
	if err := tagging.ValidatePattern(r.MatchType, r.Pattern); err != nil {
		return nil, err
	}
	desc, err := tagging.NormalizeRuleDescription(r.Description)
	if err != nil {
		return nil, err
	}
	r.Description = desc
	if err := a.deps.Rules.Create(a.ctx, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// UpdateRule validates the pattern and persists changes.
func (a *App) UpdateRule(r storage.Rule) error {
	if err := tagging.ValidatePattern(r.MatchType, r.Pattern); err != nil {
		return err
	}
	desc, err := tagging.NormalizeRuleDescription(r.Description)
	if err != nil {
		return err
	}
	r.Description = desc
	return a.deps.Rules.Update(a.ctx, &r)
}

// DeleteRule removes a rule.
func (a *App) DeleteRule(id int64) error { return a.deps.Rules.Delete(a.ctx, id) }

// TestRuleResult is a single track matched against a rule pattern.
type TestRuleResult struct {
	TrackID     int64  `json:"track_id"`
	ProcessName string `json:"process_name"`
	WindowTitle string `json:"window_title"`
	Matched     bool   `json:"matched"`
}

// TestRule evaluates the given (un-saved) rule against process tracks of
// the given UTC day.
func (a *App) TestRule(r storage.Rule, dayRFC3339 string) ([]TestRuleResult, error) {
	if err := tagging.ValidatePattern(r.MatchType, r.Pattern); err != nil {
		return nil, err
	}
	day, err := time.Parse(time.RFC3339, dayRFC3339)
	if err != nil {
		return nil, fmt.Errorf("parse day: %w", err)
	}
	tracks, err := a.deps.Tracks.ListByDay(a.ctx, day.UTC())
	if err != nil {
		return nil, err
	}
	compiled, err := tagging.Compile([]storage.Rule{r})
	if err != nil {
		return nil, err
	}
	out := make([]TestRuleResult, 0, len(tracks))
	for _, t := range tracks {
		out = append(out, TestRuleResult{
			TrackID:     t.ID,
			ProcessName: t.ProcessName,
			WindowTitle: t.WindowTitle,
			Matched:     compiled[0].Match(t.ProcessName, t.WindowTitle),
		})
	}
	return out, nil
}

// ----- Tracker control ----------------------------------------------------

// PauseTracking stops the polling loop and closes the current track.
func (a *App) PauseTracking() {
	if a.deps.Tracker != nil {
		a.deps.Tracker.Pause(a.ctx)
	}
}

// ResumeTracking re-enables polling.
func (a *App) ResumeTracking() {
	if a.deps.Tracker != nil {
		a.deps.Tracker.Resume()
	}
}

// IsTrackingPaused reports the tracker pause state.
func (a *App) IsTrackingPaused() bool {
	if a.deps.Tracker == nil {
		return true
	}
	return a.deps.Tracker.Paused()
}

// ----- Manual tagging -----------------------------------------------------

// StartManualTag opens an open-ended manual tag block. If an auto-tag is
// currently running the manual is queued (paused) and starts when the auto
// closes. Re-calling with a different tag closes the current manual first.
func (a *App) StartManualTag(tagID int64, description string) error {
	if a.deps.Orchestrator == nil {
		return errors.New("orchestrator not configured")
	}
	return a.deps.Orchestrator.StartManualOpenEnded(a.ctx, tagID, description)
}

// StopManualTag closes the open-ended manual tag (or clears the paused
// state if currently interrupted by an auto-tag).
func (a *App) StopManualTag() error {
	if a.deps.Orchestrator == nil {
		return nil
	}
	return a.deps.Orchestrator.StopManualOpenEnded(a.ctx)
}

// IsManualTagActive reports whether an open-ended manual tag is in progress
// (open or paused) and which tag it carries.
func (a *App) IsManualTagActive() (int64, bool) {
	if a.deps.Orchestrator == nil {
		return 0, false
	}
	return a.deps.Orchestrator.IsManualActive()
}

// ----- Settings -----------------------------------------------------------

// GetConfig returns the current config.
func (a *App) GetConfig() *config.Config {
	a.mu.Lock()
	defer a.mu.Unlock()
	c := config.Default()
	if a.cfg != nil {
		v := *a.cfg
		c = &v
	}
	a.logger.Debug("app: GetConfig",
		"poll_interval_sec", c.Tracking.PollIntervalSec,
		"idle_threshold_min", c.Tracking.IdleThresholdMin,
		"personio_tenant", c.Personio.Tenant)
	return c
}

// SaveConfig validates and persists a new config.
func (a *App) SaveConfig(c config.Config) error {
	rawTenant := c.Personio.Tenant
	c.Personio.Tenant = config.NormalizeTenant(rawTenant)
	c.Entra.ClientID = config.NormalizeGUID(c.Entra.ClientID)
	c.Entra.TenantID = config.NormalizeGUID(c.Entra.TenantID)
	c.Communication.ProcessNames = config.NormalizeProcessNames(c.Communication.ProcessNames)
	a.logger.Debug("app: SaveConfig requested",
		"poll_interval_sec", c.Tracking.PollIntervalSec,
		"idle_threshold_min", c.Tracking.IdleThresholdMin,
		"personio_tenant_raw", rawTenant,
		"personio_tenant", c.Personio.Tenant,
		"entra_configured", c.Entra.Configured(),
		"communication_processes", c.Communication.ProcessNames)
	if err := c.Validate(); err != nil {
		a.logger.Warn("app: SaveConfig validation failed", "err", err)
		return err
	}
	a.mu.Lock()
	prevEntra := config.EntraConfig{}
	if a.cfg != nil {
		prevEntra = a.cfg.Entra
	}
	a.mu.Unlock()
	if a.deps.ConfigPath != "" {
		if err := config.Save(a.deps.ConfigPath, &c); err != nil {
			a.logger.Error("app: SaveConfig persist failed", "err", err, "path", a.deps.ConfigPath)
			return fmt.Errorf("save config: %w", err)
		}
	}
	a.mu.Lock()
	a.cfg = &c
	a.mu.Unlock()
	if a.deps.OnConfigSet != nil {
		if err := a.deps.OnConfigSet(&c); err != nil {
			a.logger.Warn("OnConfigSet returned error", "err", err)
		}
	}
	if prevEntra != c.Entra {
		a.applyEntraConfig(c.Entra)
	}
	a.logger.Info("app: config saved", "path", a.deps.ConfigPath)
	return nil
}

// ----- Personio -----------------------------------------------------------

// PersonioSessionStatus reports the current Personio login state.
type PersonioSessionStatus struct {
	HasSession bool      `json:"has_session"`
	Tenant     string    `json:"tenant"`
	EmployeeID int64     `json:"employee_id"`
	CapturedAt time.Time `json:"captured_at"`
	Valid      bool      `json:"valid"`
	CheckedAt  time.Time `json:"checked_at,omitempty"`
	Reason     string    `json:"reason,omitempty"`
}

// PersonioStatus returns the current login state without hitting the network.
// Valid mirrors the local age check (Session.Expired) so the badge has a
// meaningful fallback when the network probe is unavailable; the network
// probe in PersonioCheck is the source of truth when it can run.
func (a *App) PersonioStatus() PersonioSessionStatus {
	if a.deps.Sessions == nil {
		return PersonioSessionStatus{Reason: "kein Session-Speicher"}
	}
	s, err := a.deps.Sessions.Get()
	if err != nil || s == nil {
		return PersonioSessionStatus{Reason: "nicht angemeldet"}
	}
	st := PersonioSessionStatus{
		HasSession: true,
		Tenant:     s.Tenant,
		EmployeeID: s.EmployeeID,
		CapturedAt: s.CapturedAt,
		Valid:      !s.Expired(),
	}
	if !st.Valid {
		st.Reason = "Session zu alt — bitte neu anmelden"
	}
	return st
}

// PersonioCheck probes the Personio app root with the stored cookies.
func (a *App) PersonioCheck() PersonioSessionStatus {
	st := a.PersonioStatus()
	if !st.HasSession {
		st.CheckedAt = time.Now().UTC()
		return st
	}
	sess, err := a.deps.Sessions.Get()
	if err != nil || sess == nil {
		st.HasSession = false
		st.Reason = "Session konnte nicht gelesen werden"
		st.CheckedAt = time.Now().UTC()
		return st
	}
	validate := a.validatePersonio
	if validate == nil {
		validate = personio.Validate
	}
	if err := validate(a.ctx, sess); err != nil {
		a.logger.Info("app: PersonioCheck — session invalid", "err", err)
		// When Personio rejects the cookies the locally cached blob is
		// dead by observation — drop it so every downstream consumer
		// (auto-relogin's slow-path, plugins calling
		// RequestPersonioSession, the badge itself on the next probe)
		// stops trusting Session.Expired()'s 24h heuristic and treats
		// the state as "no session". Without this purge, an
		// AutoRelogin trigger would short-circuit through
		// personioSessionSource.EnsureSession's fast path (which only
		// looks at CapturedAt age) and never actually open Chrome.
		// Other failure modes (5xx, unexpected redirects, network
		// errors) keep the cookies intact: they may still be valid.
		if errors.Is(err, personio.ErrSessionExpired) {
			if delErr := a.deps.Sessions.Delete(); delErr != nil {
				a.logger.Warn("app: PersonioCheck — could not purge stale session", "err", delErr)
			}
		}
		st.Valid = false
		st.Reason = "Session abgelaufen"
		st.CheckedAt = time.Now().UTC()
		a.maybeTriggerAutoRelogin()
		return st
	}
	st.Valid = true
	st.CheckedAt = time.Now().UTC()
	return st
}

// maybeTriggerAutoRelogin fires a fire-and-forget CDP login when the
// user has opted in via Personio.AutoRelogin. The CAS guard inside the
// session source absorbs the repeated minute-tick PersonioCheck calls
// that happen while the previous login is still waiting for the user.
func (a *App) maybeTriggerAutoRelogin() {
	if a.personioSrc == nil {
		return
	}
	a.mu.Lock()
	enabled := a.cfg != nil && a.cfg.Personio.AutoRelogin
	a.mu.Unlock()
	if !enabled {
		return
	}
	a.personioSrc.TriggerAutoRelogin()
}

// PersonioLogin launches an interactive Chrome session for login.
func (a *App) PersonioLogin() error {
	cfg := a.GetConfig()
	tenant := strings.TrimSpace(cfg.Personio.Tenant)
	a.logger.Info("app: PersonioLogin started", "tenant", tenant)
	if tenant == "" {
		return errors.New("kein Personio-Tenant in den Einstellungen hinterlegt")
	}
	if a.deps.Sessions == nil {
		return errors.New("session store nicht verfügbar")
	}

	res, err := personio.Login(a.ctx, personio.LoginConfig{
		Tenant:  tenant,
		Logger:  a.logger,
		Timeout: 5 * time.Minute,
	})
	if err != nil {
		return fmt.Errorf("personio login: %w", err)
	}

	if err := personio.Validate(a.ctx, res.Session); err != nil {
		return fmt.Errorf("validierung der erfassten Session fehlgeschlagen: %w", err)
	}

	if cli, err := personio.NewUIClient(personio.UIClientOptions{Session: res.Session, Logger: a.logger}); err == nil {
		if eid, err := cli.FetchEmployeeID(a.ctx); err == nil && eid != 0 {
			res.Session.EmployeeID = eid
		} else if err != nil {
			a.logger.Warn("could not pre-resolve employee id", "err", err)
		}
	}

	if err := a.deps.Sessions.Set(res.Session); err != nil {
		return fmt.Errorf("session speichern: %w", err)
	}
	a.logger.Info("personio login: session stored",
		"tenant", res.Session.Tenant,
		"app_host", res.Session.AppHost,
		"employee_id", res.Session.EmployeeID)
	return nil
}

// PersonioLogout deletes the persisted session.
func (a *App) PersonioLogout() error {
	if a.deps.Sessions == nil {
		return nil
	}
	return a.deps.Sessions.Delete()
}

// ----- Entra ID -----------------------------------------------------------

// EntraStatusResponse is the JSON shape the Settings tab consumes. The
// "configured" flag drives whether the Login button is enabled at all;
// "has_account" drives the badge between "nicht angemeldet" and the
// signed-in info card.
type EntraStatusResponse struct {
	Configured    bool   `json:"configured"`
	HasAccount    bool   `json:"has_account"`
	Username      string `json:"username,omitempty"`
	HomeAccountID string `json:"home_account_id,omitempty"`
	TenantID      string `json:"tenant_id,omitempty"`
	ClientID      string `json:"client_id,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

// EntraStatus reports the Entra ID auth state without hitting the
// network. Returns "feature off" with a reason string when ClientID /
// TenantID are unset or the manager failed to construct.
func (a *App) EntraStatus() EntraStatusResponse {
	mgr := a.currentEntra()
	if mgr == nil || !mgr.Configured() {
		return EntraStatusResponse{Reason: "Entra ID nicht konfiguriert"}
	}
	st := mgr.Status(a.ctx)
	return EntraStatusResponse{
		Configured:    true,
		HasAccount:    st.HasAccount,
		Username:      st.Username,
		HomeAccountID: st.HomeAccountID,
		TenantID:      st.TenantID,
		ClientID:      st.ClientID,
	}
}

// EntraLogin runs an interactive browser login with the default Graph
// scopes. On Entra-joined Windows the Edge/system-browser PRT-SSO makes
// this promptless; otherwise the user authenticates as usual.
func (a *App) EntraLogin() error {
	mgr := a.currentEntra()
	if mgr == nil || !mgr.Configured() {
		// User-facing German message surfaced directly via Wails binding;
		// "Entra ID" is the Microsoft product name and stays capitalised.
		return errors.New("Entra ID ist nicht konfiguriert — bitte Client- und Tenant-ID eintragen") //nolint:staticcheck // ST1005: deliberate, proper-noun start
	}
	a.logger.Info("app: EntraLogin started")
	if err := mgr.Login(a.ctx, nil); err != nil {
		a.logger.Warn("app: EntraLogin failed", "err", err)
		return fmt.Errorf("entra login: %w", err)
	}
	a.logger.Info("app: EntraLogin completed")
	return nil
}

// EntraLogout removes the cached account and deletes the encrypted cache
// blob. The Windows session is unaffected.
func (a *App) EntraLogout() error {
	mgr := a.currentEntra()
	if mgr == nil {
		return nil
	}
	return mgr.Logout(a.ctx)
}

// currentEntra returns the live Entra manager under entraMu so reads are
// safe against SaveConfig swaps. Returns nil when the feature is off.
func (a *App) currentEntra() entra.Manager {
	a.entraMu.Lock()
	defer a.entraMu.Unlock()
	return a.entraMgr
}

// currentEntraTokenSource is the EntraSource lambda the plugin host
// holds. Returns a pluginhost.EntraTokenSource that the bound HostAPI
// can invoke on every plugin RequestEntraToken call. Returns nil when
// no manager is configured (the bound API then surfaces
// sdk.ErrEntraNotAvailable to plugins). entra.Manager satisfies the
// narrow EntraTokenSource interface via duck typing, so no wrapper is
// needed beyond the nil guard.
func (a *App) currentEntraTokenSource() pluginhost.EntraTokenSource {
	a.entraMu.Lock()
	defer a.entraMu.Unlock()
	if a.entraMgr == nil {
		return nil
	}
	return a.entraMgr
}

// applyEntraConfig swaps the Entra manager when client_id / tenant_id
// changed. Called from SaveConfig with the new (already-validated) config.
// The previous manager (if any) is dropped — its in-memory cache is
// abandoned, but the on-disk blob remains so the user keeps their
// previous identity should they revert the configuration. Switching to
// an unconfigured state nils out the manager.
func (a *App) applyEntraConfig(cfg config.EntraConfig) {
	a.entraMu.Lock()
	defer a.entraMu.Unlock()

	if !cfg.Configured() {
		if a.entraMgr != nil {
			a.logger.Info("entra: feature disabled via config — dropping live manager")
		}
		a.entraMgr = nil
		return
	}
	if a.deps.EntraFor == nil {
		a.logger.Warn("entra: EntraFor wiring missing — manager not rebuilt")
		return
	}
	mgr, err := a.deps.EntraFor(cfg)
	if err != nil {
		a.logger.Warn("entra: rebuild after config change failed",
			"err", err)
		a.entraMgr = nil
		return
	}
	a.entraMgr = mgr
	a.logger.Info("entra: manager rebuilt from new config")
}

// SyncDay triggers a sync of the given UTC day.
func (a *App) SyncDay(dayRFC3339 string) (*personio.Result, error) {
	day, err := time.Parse(time.RFC3339, dayRFC3339)
	if err != nil {
		return nil, fmt.Errorf("parse day: %w", err)
	}
	a.logger.Info("app: SyncDay", "day", day.Format("2006-01-02"))
	syncer, err := a.currentSyncer()
	if err != nil {
		return nil, err
	}
	res, err := syncer.SyncDay(a.ctx, day.UTC())
	if err != nil {
		a.logger.Error("app: SyncDay failed", "err", err)
		return res, err
	}
	a.logger.Info("app: SyncDay done",
		"periods", res.Periods, "blocks", res.BlocksProcessed,
		"skipped", res.BlocksSkipped, "errors", len(res.Errors))
	return res, nil
}

// SyncRange triggers a sync of the given range.
func (a *App) SyncRange(fromRFC3339, toRFC3339 string) (*personio.Result, error) {
	from, err := time.Parse(time.RFC3339, fromRFC3339)
	if err != nil {
		return nil, err
	}
	to, err := time.Parse(time.RFC3339, toRFC3339)
	if err != nil {
		return nil, err
	}
	syncer, err := a.currentSyncer()
	if err != nil {
		return nil, err
	}
	return syncer.SyncRange(a.ctx, from.UTC(), to.UTC())
}

// RequestSyncToday is the tray-driven sync entry point for today's UTC
// day. Mirrors the startup-sync's preflight-then-push pattern: if
// Personio already has work-type periods, the override/import modal is
// surfaced (and the main window brought forward so the user sees it)
// instead of silently clobbering them. On a clean preflight, the sync
// runs the same override path the manual button uses. Logged but
// otherwise silent on failure — the tray gives no other UI feedback.
func (a *App) RequestSyncToday() {
	todayISO := time.Now().UTC().Format(time.RFC3339)
	pre, err := a.PreflightSyncDay(todayISO)
	if err != nil {
		a.logger.Warn("tray sync: preflight failed", "err", err)
		return
	}
	if pre.HasExistingPeriods() || !pre.Trackable {
		a.logger.Info("tray sync: existing periods or non-trackable — surfacing modal",
			"day", pre.Day, "existing", len(pre.ExistingPeriods), "state", pre.State)
		a.ShowWindow()
		a.emitStartupConflict(pre)
		return
	}
	if _, err := a.SyncDay(todayISO); err != nil {
		a.logger.Warn("tray sync: failed", "err", err)
	}
}

// PreflightSyncDay returns what Personio currently has on the given day so
// the frontend can warn before the override sync wipes it. Existing
// work-type periods come back in the response; empty means "safe to push".
func (a *App) PreflightSyncDay(dayRFC3339 string) (*personio.SyncPreflight, error) {
	day, err := time.Parse(time.RFC3339, dayRFC3339)
	if err != nil {
		return nil, fmt.Errorf("parse day: %w", err)
	}
	a.logger.Info("app: PreflightSyncDay", "day", day.Format("2006-01-02"))
	syncer, err := a.currentSyncer()
	if err != nil {
		return nil, err
	}
	return syncer.Preflight(a.ctx, day.UTC())
}

// ImportPersonioDay pulls existing Personio periods for the given day into
// the local tag-block table. Periods are trimmed against existing local
// blocks (local blocks win) and inserted with a tag resolved from
// personio_project_id, falling back to a placeholder tag.
func (a *App) ImportPersonioDay(dayRFC3339 string) (*personio.ImportResult, error) {
	day, err := time.Parse(time.RFC3339, dayRFC3339)
	if err != nil {
		return nil, fmt.Errorf("parse day: %w", err)
	}
	a.logger.Info("app: ImportPersonioDay", "day", day.Format("2006-01-02"))
	syncer, err := a.currentSyncer()
	if err != nil {
		return nil, err
	}
	res, err := syncer.ImportDay(a.ctx, day.UTC())
	if err != nil {
		a.logger.Error("app: ImportPersonioDay failed", "err", err)
		return res, err
	}
	a.logger.Info("app: ImportPersonioDay done",
		"considered", res.PeriodsConsidered,
		"blocks_created", res.BlocksCreated,
		"skipped", res.PeriodsSkipped,
		"errors", len(res.Errors))
	return res, nil
}

// ----- Quick-tag-picker --------------------------------------------------

// QuickTagSlot is a single entry in the quick-tag-picker. Up to ten are
// returned, numbered 0..9. IsActive marks the currently open or paused
// manual tag so the picker can highlight it.
type QuickTagSlot struct {
	Index    int     `json:"index"`
	TagID    int64   `json:"tag_id"`
	Label    string  `json:"label"`
	Color    *string `json:"color,omitempty"`
	IsActive bool    `json:"is_active"`
}

// QuickTagSlots returns the picker entries: recently-used tags first
// (most-recent block within the last 30 days), then unused tags ordered
// parent-first to mirror the rest of the UI. Capped at 10 entries.
func (a *App) QuickTagSlots() ([]QuickTagSlot, error) {
	since := time.Now().UTC().AddDate(0, 0, -quickTagRecentDays)
	recent, err := a.deps.TagBlocks.RecentlyUsedTagIDs(a.ctx, since, quickTagSlotCount)
	if err != nil {
		return nil, fmt.Errorf("recent tags: %w", err)
	}
	tags, err := a.deps.Tags.List(a.ctx)
	if err != nil {
		return nil, fmt.Errorf("list tags: %w", err)
	}
	tagsByID := make(map[int64]storage.Tag, len(tags))
	for _, t := range tags {
		tagsByID[t.ID] = t
	}
	activeTagID, _ := a.IsManualTagActive()

	seen := make(map[int64]bool, quickTagSlotCount)
	slots := make([]QuickTagSlot, 0, quickTagSlotCount)
	add := func(t storage.Tag) {
		if len(slots) >= quickTagSlotCount || seen[t.ID] {
			return
		}
		seen[t.ID] = true
		label := t.Name
		if t.ParentID != nil {
			if p, ok := tagsByID[*t.ParentID]; ok {
				label = p.Name + " › " + t.Name
			}
		}
		slots = append(slots, QuickTagSlot{
			Index:    len(slots),
			TagID:    t.ID,
			Label:    label,
			Color:    t.Color,
			IsActive: activeTagID > 0 && t.ID == activeTagID,
		})
	}

	for _, id := range recent {
		if t, ok := tagsByID[id]; ok {
			add(t)
		}
	}
	for _, t := range orderedTagsForFill(tags) {
		add(t)
	}
	return slots, nil
}

// orderedTagsForFill orders tags parent-first so the fill order mirrors
// the timeline picker and tray submenu (parent followed by its children).
func orderedTagsForFill(tags []storage.Tag) []storage.Tag {
	parents := make([]storage.Tag, 0, len(tags))
	childrenByParent := make(map[int64][]storage.Tag)
	for _, t := range tags {
		if t.ParentID == nil {
			parents = append(parents, t)
			continue
		}
		childrenByParent[*t.ParentID] = append(childrenByParent[*t.ParentID], t)
	}
	out := make([]storage.Tag, 0, len(tags))
	for _, p := range parents {
		out = append(out, p)
		out = append(out, childrenByParent[p.ID]...)
	}
	// Orphan subtags (parent missing) — emit at the end so they remain
	// visible even when their parent has been deleted.
	for _, t := range tags {
		if t.ParentID == nil {
			continue
		}
		if _, ok := childrenByParent[*t.ParentID]; !ok {
			out = append(out, t)
		}
	}
	return out
}

// QuickTagOpen surfaces the quick-tag-picker. Triggered by the global
// hotkey handler. Saves the current main-window placement, resizes the
// window into a small popup at the cursor monitor's bottom-right, and
// emits a frontend event so the picker UI mounts. Idempotent: a second
// open while the picker is already up just brings it back to front.
func (a *App) QuickTagOpen() error {
	a.mu.Lock()
	ctx, ready := a.ctx, a.started
	if !ready || ctx == nil {
		a.mu.Unlock()
		return errors.New("frontend not ready")
	}
	already := a.quickTagState.saved
	if !already {
		w, h := wailsruntime.WindowGetSize(ctx)
		x, y := wailsruntime.WindowGetPosition(ctx)
		a.quickTagState = quickTagWindowState{
			saved:      true,
			wasVisible: a.windowVisible,
			width:      w,
			height:     h,
			x:          x,
			y:          y,
		}
	}
	a.windowVisible = true
	a.mu.Unlock()

	if work, err := winapi.CursorMonitorWorkArea(); err == nil {
		px := int(work.Right) - quickTagPickerWidth - quickTagPickerMargin
		py := int(work.Bottom) - quickTagPickerHeight - quickTagPickerMargin
		wailsruntime.WindowSetPosition(ctx, px, py)
	} else {
		a.logger.Warn("quick tag: cursor monitor lookup failed", "err", err)
		wailsruntime.WindowCenter(ctx)
	}
	wailsruntime.WindowSetSize(ctx, quickTagPickerWidth, quickTagPickerHeight)
	wailsruntime.WindowSetAlwaysOnTop(ctx, true)
	wailsruntime.WindowShow(ctx)
	wailsruntime.WindowUnminimise(ctx)
	wailsruntime.EventsEmit(ctx, quickTagOpenEvent)
	a.logger.Debug("quick tag: opened", "already", already)
	return nil
}

// QuickTagDismiss closes the picker without changing the active tag.
func (a *App) QuickTagDismiss() {
	a.closeQuickTagWindow()
}

// QuickTagSelect picks the tag at the given id. No-op when the tag is
// already the active manual tag (matches the spec). Always closes the
// picker afterwards.
func (a *App) QuickTagSelect(tagID int64) error {
	if tagID <= 0 {
		a.closeQuickTagWindow()
		return fmt.Errorf("invalid tag id: %d", tagID)
	}
	activeTagID, _ := a.IsManualTagActive()
	if tagID != activeTagID {
		a.logger.Info("quick tag: switch", "tag_id", tagID, "previous", activeTagID)
		if err := a.StartManualTag(tagID, ""); err != nil {
			a.closeQuickTagWindow()
			return err
		}
	} else {
		a.logger.Debug("quick tag: same tag selected — no-op", "tag_id", tagID)
	}
	a.closeQuickTagWindow()
	return nil
}

func (a *App) closeQuickTagWindow() {
	a.mu.Lock()
	ctx, ready := a.ctx, a.started
	state := a.quickTagState
	a.quickTagState = quickTagWindowState{}
	if !state.wasVisible {
		a.windowVisible = false
	}
	a.mu.Unlock()
	if !ready || ctx == nil {
		return
	}
	wailsruntime.EventsEmit(ctx, quickTagCloseEvent)
	wailsruntime.WindowSetAlwaysOnTop(ctx, false)
	if state.saved {
		wailsruntime.WindowSetSize(ctx, state.width, state.height)
		wailsruntime.WindowSetPosition(ctx, state.x, state.y)
	}
	if !state.wasVisible {
		wailsruntime.WindowHide(ctx)
	}
}

// FireQuickTag is the application-internal entry the hotkey handler calls.
// Toggle semantics: a second press while the picker is open dismisses it,
// matching the muscle-memory expectation users have from system pickers.
func (a *App) FireQuickTag() {
	a.mu.Lock()
	open := a.quickTagState.saved
	a.mu.Unlock()
	if open {
		a.closeQuickTagWindow()
		return
	}
	if err := a.QuickTagOpen(); err != nil {
		a.logger.Warn("quick tag fire: open failed", "err", err)
	}
}

func (a *App) currentSyncer() (*personio.Syncer, error) {
	if a.deps.Sessions == nil || a.deps.SyncerFor == nil {
		return nil, errors.New("personio nicht konfiguriert")
	}
	sess, err := a.deps.Sessions.Get()
	if err != nil {
		return nil, fmt.Errorf("keine Session — bitte über Einstellungen anmelden: %w", err)
	}
	syncer := a.deps.SyncerFor(sess)
	if syncer == nil {
		return nil, errors.New("personio: Syncer konnte nicht aufgebaut werden (Tenant gesetzt?)")
	}
	return syncer, nil
}

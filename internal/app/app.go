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

	"github.com/onesi/hashpoint/internal/config"
	"github.com/onesi/hashpoint/internal/personio"
	"github.com/onesi/hashpoint/internal/storage"
	"github.com/onesi/hashpoint/internal/tagging"
	"github.com/onesi/hashpoint/internal/tracker"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

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
	Tracker      *tracker.Tracker
	Orchestrator *tagging.Orchestrator
	Sessions     personio.SessionStore
	// SyncerFor returns a Syncer wired against the given session, or nil if
	// the session is not usable (e.g. tenant unset). Constructed lazily so
	// session changes from the UI take effect immediately.
	SyncerFor   func(*personio.Session) *personio.Syncer
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

	mu      sync.Mutex
	cfg     *config.Config
	started bool
}

// New constructs the app from its dependencies.
func New(deps Deps) *App {
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	return &App{deps: deps, logger: deps.Logger, cfg: deps.Config, ctx: context.Background()}
}

// Startup is invoked by Wails once the runtime is ready. We piggy-back on
// it to close any dangling open-ended manual tag block left over from a
// previous run — the orchestrator computes the correct close time using
// the last process-track end (or `now` when tracking is disabled).
func (a *App) Startup(ctx context.Context) {
	a.mu.Lock()
	a.ctx = ctx
	a.started = true
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
	_ = cfg
}

// Shutdown is invoked by Wails on window close. Tracker shutdown is handled
// in main; nothing to do here.
func (a *App) Shutdown(_ context.Context) {}

// ShowWindow brings the Wails main window to the foreground.
func (a *App) ShowWindow() {
	a.mu.Lock()
	ctx, ready := a.ctx, a.started
	a.mu.Unlock()
	if !ready || ctx == nil {
		a.logger.Warn("app: ShowWindow called before Wails Startup — ignoring")
		return
	}
	wailsruntime.WindowShow(ctx)
	wailsruntime.WindowUnminimise(ctx)
}

// Quit triggers a graceful Wails shutdown so OnShutdown runs (closing open
// blocks and flushing today's sync to Personio). Returns false when Wails
// has not finished Startup yet — in that case the caller must fall back to
// cancelling the root context directly.
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
// rejects overlap with existing manual blocks.
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
	return a.deps.Orchestrator.CreateManualRange(a.ctx, tagID, description, start.UTC(), end.UTC())
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

// SetTagBlockTag re-points an existing tag block to a different tag.
func (a *App) SetTagBlockTag(id, tagID int64) error {
	if tagID <= 0 {
		return fmt.Errorf("invalid tag id: %d", tagID)
	}
	return a.deps.TagBlocks.SetTag(a.ctx, id, tagID)
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

// DeleteTag removes a tag and its sub-tags.
func (a *App) DeleteTag(id int64) error { return a.deps.Tags.Delete(a.ctx, id) }

// ----- Rules --------------------------------------------------------------

// ListRules returns all auto-tag rules.
func (a *App) ListRules() ([]storage.Rule, error) { return a.deps.Rules.List(a.ctx) }

// CreateRule validates the pattern and inserts a rule.
func (a *App) CreateRule(r storage.Rule) (*storage.Rule, error) {
	if err := tagging.ValidatePattern(r.MatchType, r.Pattern); err != nil {
		return nil, err
	}
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
		"personio_tenant", c.Personio.Tenant,
		"autostart", c.UI.Autostart)
	return c
}

// SaveConfig validates and persists a new config.
func (a *App) SaveConfig(c config.Config) error {
	rawTenant := c.Personio.Tenant
	c.Personio.Tenant = config.NormalizeTenant(rawTenant)
	a.logger.Debug("app: SaveConfig requested",
		"poll_interval_sec", c.Tracking.PollIntervalSec,
		"idle_threshold_min", c.Tracking.IdleThresholdMin,
		"personio_tenant_raw", rawTenant,
		"personio_tenant", c.Personio.Tenant,
		"autostart", c.UI.Autostart)
	if err := c.Validate(); err != nil {
		a.logger.Warn("app: SaveConfig validation failed", "err", err)
		return err
	}
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
func (a *App) PersonioStatus() PersonioSessionStatus {
	if a.deps.Sessions == nil {
		return PersonioSessionStatus{Reason: "kein Session-Speicher"}
	}
	s, err := a.deps.Sessions.Get()
	if err != nil || s == nil {
		return PersonioSessionStatus{Reason: "nicht angemeldet"}
	}
	return PersonioSessionStatus{
		HasSession: true,
		Tenant:     s.Tenant,
		EmployeeID: s.EmployeeID,
		CapturedAt: s.CapturedAt,
	}
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
	if err := personio.Validate(a.ctx, sess); err != nil {
		a.logger.Info("app: PersonioCheck — session invalid", "err", err)
		st.Valid = false
		st.Reason = "Session abgelaufen"
		st.CheckedAt = time.Now().UTC()
		return st
	}
	st.Valid = true
	st.CheckedAt = time.Now().UTC()
	return st
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

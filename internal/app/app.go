// Package app exposes Wails-bound methods to the frontend. The App struct
// is the single bridge between the JS layer and the Go backend; no other
// package speaks to Wails directly.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
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
	Blocks   storage.FocusBlockRepository
	Tags     storage.TagRepository
	Rules    storage.RuleRepository
	Settings storage.SettingsRepository
	Tracker  *tracker.Tracker
	Sessions personio.SessionStore
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

	mu  sync.Mutex
	cfg *config.Config
	// started flips true once Wails has called Startup with the real runtime
	// context. Guarded by `mu`. We need it because the tray goroutine may
	// fire ShowWindow before the Wails frontend has attached itself —
	// runtime.* helpers panic if handed a context without their hidden
	// frontend value.
	started bool
	// Manual-tag state — guarded by `mu`. When `manualBlockID` is non-nil a
	// placeholder block is currently open under the user's selected tag from
	// the tray submenu. The tracker is told the active tag id via
	// SetManualTag so any program blocks it opens while polling pick the
	// same tag instead of running through the auto-tag rule engine — process
	// tracking and auto-tagging stay live whenever tracking is enabled, the
	// user's explicit manual choice just overrides the rules for the
	// duration of the manual block.
	manualBlockID *int64
}

// New constructs the app from its dependencies.
func New(deps Deps) *App {
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	// Seed `ctx` with a non-nil background context so methods invoked before
	// Wails calls Startup (notably the tray goroutine, which lists tags to
	// build its submenu) don't dereference a nil ctx in database/sql and
	// crash the whole process.
	return &App{deps: deps, logger: deps.Logger, cfg: deps.Config, ctx: context.Background()}
}

// Startup is invoked by Wails once the runtime is ready.
func (a *App) Startup(ctx context.Context) {
	a.mu.Lock()
	a.ctx = ctx
	a.started = true
	a.mu.Unlock()
	a.logger.Info("frontend started")
}

// Shutdown is invoked by Wails on window close. Tracker shutdown is handled
// in main; nothing to do here.
func (a *App) Shutdown(_ context.Context) {}

// ShowWindow brings the Wails main window to the foreground. The tray's
// "Öffnen" entry calls it after the user has closed the window — Wails is
// configured with HideWindowOnClose, so close hides the window and the
// tray is the only way to bring it back without restarting the app. If
// Startup has not run yet (tray click during early boot) we just log: the
// runtime helpers would otherwise panic on a context without their
// frontend value.
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

// Version returns build metadata for the "About" dialog.
func (a *App) Version() VersionInfo { return a.deps.Version }

// LogFrontend ships a log record from the React layer into the same slog
// pipeline as the rest of the app. Used by window.onerror, unhandledrejection
// handlers and a thin console.error/warn forwarder. Levels are kept loose:
// anything not matching error/warn/info/debug lands at INFO.
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

// ----- Blocks -------------------------------------------------------------

// BlocksByDay returns all focus blocks on the given UTC day (RFC3339).
func (a *App) BlocksByDay(dayRFC3339 string) ([]storage.FocusBlock, error) {
	day, err := time.Parse(time.RFC3339, dayRFC3339)
	if err != nil {
		return nil, fmt.Errorf("parse day: %w", err)
	}
	return a.deps.Blocks.ListByDay(a.ctx, day.UTC())
}

// BlocksBetween returns blocks in the [from, to) range.
func (a *App) BlocksBetween(fromRFC3339, toRFC3339 string) ([]storage.FocusBlock, error) {
	from, err := time.Parse(time.RFC3339, fromRFC3339)
	if err != nil {
		return nil, fmt.Errorf("parse from: %w", err)
	}
	to, err := time.Parse(time.RFC3339, toRFC3339)
	if err != nil {
		return nil, fmt.Errorf("parse to: %w", err)
	}
	return a.deps.Blocks.ListBetween(a.ctx, from.UTC(), to.UTC())
}

// AssignTag assigns a tag (or clears it with tagID == 0) to multiple blocks.
func (a *App) AssignTag(blockIDs []int64, tagID int64) error {
	var ptr *int64
	if tagID > 0 {
		ptr = &tagID
	}
	for _, id := range blockIDs {
		if err := a.deps.Blocks.SetTag(a.ctx, id, ptr, false); err != nil {
			return fmt.Errorf("set tag for block %d: %w", id, err)
		}
	}
	return nil
}

// AssignTagAndDescription is the bulk-tagging primitive used by the Timeline:
// it sets the same tag and description on every block in blockIDs. tagID == 0
// clears the tag; description == "" clears the description. Setting only
// description is supported by passing tagID == -1 (leave tag untouched).
//
// rangeStartRFC3339 / rangeEndRFC3339 are optional ("" = ignore). When set
// alongside a positive tagID, any portion of [rangeStart, rangeEnd) not
// already covered by a non-idle block in blockIDs is filled with synthetic
// "placeholder" blocks tagged with tagID — extending the period synced to
// Personio past actually tracked activity. When tagID == 0 (clear), any
// blockIDs that are placeholders are deleted again.
func (a *App) AssignTagAndDescription(blockIDs []int64, tagID int64, description, rangeStartRFC3339, rangeEndRFC3339 string) error {
	var (
		tagPtr   *int64
		setTag   = true
		descPtr  *string
		descTrim = strings.TrimSpace(description)
	)
	switch {
	case tagID < 0:
		setTag = false
	case tagID > 0:
		tagPtr = &tagID
	}
	if descTrim != "" {
		descPtr = &descTrim
	}

	// Path A: clearing the tag — placeholder blocks among the IDs are deleted
	// rather than left behind as orphaned synthetic gaps.
	if setTag && tagID == 0 {
		for _, id := range blockIDs {
			b, err := a.deps.Blocks.Get(a.ctx, id)
			if err != nil {
				return fmt.Errorf("get block %d: %w", id, err)
			}
			if b == nil {
				continue
			}
			if b.IsPlaceholder {
				if err := a.deps.Blocks.Delete(a.ctx, id); err != nil {
					return fmt.Errorf("delete placeholder %d: %w", id, err)
				}
				continue
			}
			if err := a.deps.Blocks.SetTag(a.ctx, id, nil, false); err != nil {
				return fmt.Errorf("clear tag for block %d: %w", id, err)
			}
			if err := a.deps.Blocks.SetDescription(a.ctx, id, descPtr); err != nil {
				return fmt.Errorf("set description for block %d: %w", id, err)
			}
		}
		return nil
	}

	// Path B: tag set with explicit range — fill uncovered portions with
	// placeholder blocks before tagging the existing ones.
	if setTag && tagID > 0 && rangeStartRFC3339 != "" && rangeEndRFC3339 != "" {
		rs, err := time.Parse(time.RFC3339, rangeStartRFC3339)
		if err != nil {
			return fmt.Errorf("parse range_start: %w", err)
		}
		re, err := time.Parse(time.RFC3339, rangeEndRFC3339)
		if err != nil {
			return fmt.Errorf("parse range_end: %w", err)
		}
		rs = rs.UTC()
		re = re.UTC()
		if re.After(rs) {
			covers := make([]storage.FocusBlock, 0, len(blockIDs))
			for _, id := range blockIDs {
				b, err := a.deps.Blocks.Get(a.ctx, id)
				if err != nil {
					return fmt.Errorf("get block %d: %w", id, err)
				}
				if b == nil || b.IsIdle || b.EndTime == nil {
					continue
				}
				covers = append(covers, *b)
			}
			sort.Slice(covers, func(i, j int) bool {
				return covers[i].StartTime.Before(covers[j].StartTime)
			})
			for _, gap := range computeRangeGaps(rs, re, covers) {
				ph := &storage.FocusBlock{
					ProcessName:   "",
					WindowTitle:   "",
					StartTime:     gap.start,
					EndTime:       &gap.end,
					DurationSec:   int64(gap.end.Sub(gap.start).Round(time.Second).Seconds()),
					TagID:         tagPtr,
					IsPlaceholder: true,
					Description:   descPtr,
				}
				if err := a.deps.Blocks.Open(a.ctx, ph); err != nil {
					return fmt.Errorf("create placeholder block: %w", err)
				}
			}
		}
	}

	// Apply tag + description to the existing IDs (placeholders get re-tagged
	// alongside real blocks if they were already in the selection).
	for _, id := range blockIDs {
		if setTag {
			if err := a.deps.Blocks.SetTag(a.ctx, id, tagPtr, false); err != nil {
				return fmt.Errorf("set tag for block %d: %w", id, err)
			}
		}
		if err := a.deps.Blocks.SetDescription(a.ctx, id, descPtr); err != nil {
			return fmt.Errorf("set description for block %d: %w", id, err)
		}
	}
	return nil
}

type rangeGap struct{ start, end time.Time }

// computeRangeGaps returns the sub-intervals of [rs, re) that are not covered
// by any block in covers. covers must be sorted ascending by StartTime and
// contain only closed, non-idle blocks.
func computeRangeGaps(rs, re time.Time, covers []storage.FocusBlock) []rangeGap {
	var gaps []rangeGap
	cursor := rs
	for _, b := range covers {
		if b.EndTime == nil {
			continue
		}
		bs := b.StartTime
		be := *b.EndTime
		if !be.After(rs) || !bs.Before(re) {
			continue
		}
		if bs.After(cursor) {
			end := bs
			if end.After(re) {
				end = re
			}
			if end.After(cursor) {
				gaps = append(gaps, rangeGap{start: cursor, end: end})
			}
		}
		if be.After(cursor) {
			cursor = be
		}
		if !cursor.Before(re) {
			cursor = re
			break
		}
	}
	if cursor.Before(re) {
		gaps = append(gaps, rangeGap{start: cursor, end: re})
	}
	return gaps
}

// SetBlockDescription updates the description on a single block.
func (a *App) SetBlockDescription(id int64, description string) error {
	d := strings.TrimSpace(description)
	var ptr *string
	if d != "" {
		ptr = &d
	}
	return a.deps.Blocks.SetDescription(a.ctx, id, ptr)
}

// SplitBlock splits a block at the given UTC time.
func (a *App) SplitBlock(id int64, atRFC3339 string) (*storage.FocusBlock, error) {
	at, err := time.Parse(time.RFC3339, atRFC3339)
	if err != nil {
		return nil, fmt.Errorf("parse at: %w", err)
	}
	return a.deps.Blocks.Split(a.ctx, id, at.UTC())
}

// UpdateBlock writes the editable fields back.
func (a *App) UpdateBlock(b storage.FocusBlock) error {
	return a.deps.Blocks.Update(a.ctx, &b)
}

// DeleteBlock removes a single block.
func (a *App) DeleteBlock(id int64) error {
	a.logger.Info("app: DeleteBlock", "id", id)
	if err := a.deps.Blocks.Delete(a.ctx, id); err != nil {
		a.logger.Warn("app: DeleteBlock failed", "id", id, "err", err)
		return err
	}
	return nil
}

// DeleteBlocks removes a batch of blocks. Real blocks are dropped from the
// table together with any placeholder blocks in the same set — the timeline
// "Löschen" button uses this to wipe the user's selection in one shot. Returns
// the number of rows actually removed (rows that were already gone are
// silently ignored).
func (a *App) DeleteBlocks(ids []int64) (int, error) {
	a.logger.Info("app: DeleteBlocks requested", "count", len(ids), "ids", ids)
	if len(ids) == 0 {
		return 0, nil
	}
	deleted := 0
	for _, id := range ids {
		if err := a.deps.Blocks.Delete(a.ctx, id); err != nil {
			a.logger.Warn("app: DeleteBlocks delete failed",
				"id", id, "deleted_before_failure", deleted, "err", err)
			return deleted, fmt.Errorf("delete block %d: %w", id, err)
		}
		deleted++
	}
	a.logger.Info("app: DeleteBlocks done", "requested", len(ids), "deleted", deleted)
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

// ListRules returns all auto-tag rules ordered by priority DESC.
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

// TestRuleResult is a single block matched against a rule pattern (for the
// rules-management UI).
type TestRuleResult struct {
	BlockID     int64  `json:"block_id"`
	ProcessName string `json:"process_name"`
	WindowTitle string `json:"window_title"`
	Matched     bool   `json:"matched"`
}

// TestRule evaluates the given (un-saved) rule against the blocks of the
// given UTC day. Useful for the live-test UI.
func (a *App) TestRule(r storage.Rule, dayRFC3339 string) ([]TestRuleResult, error) {
	if err := tagging.ValidatePattern(r.MatchType, r.Pattern); err != nil {
		return nil, err
	}
	day, err := time.Parse(time.RFC3339, dayRFC3339)
	if err != nil {
		return nil, fmt.Errorf("parse day: %w", err)
	}
	blocks, err := a.deps.Blocks.ListByDay(a.ctx, day.UTC())
	if err != nil {
		return nil, err
	}
	compiled, err := tagging.Compile([]storage.Rule{r})
	if err != nil {
		return nil, err
	}
	out := make([]TestRuleResult, 0, len(blocks))
	for _, b := range blocks {
		out = append(out, TestRuleResult{
			BlockID:     b.ID,
			ProcessName: b.ProcessName,
			WindowTitle: b.WindowTitle,
			Matched:     compiled[0].Match(b.ProcessName, b.WindowTitle),
		})
	}
	return out, nil
}

// ApplyRuleToHistory runs a saved rule retroactively against all untagged blocks.
// Returns the count of blocks newly tagged.
func (a *App) ApplyRuleToHistory(ruleID int64) (int, error) {
	r, err := a.deps.Rules.Get(a.ctx, ruleID)
	if err != nil {
		return 0, err
	}
	if r == nil {
		return 0, errors.New("rule not found")
	}
	compiled, err := tagging.Compile([]storage.Rule{*r})
	if err != nil {
		return 0, err
	}
	// Use a wide range for "history". A user can always re-apply later.
	from := time.Now().AddDate(-2, 0, 0).UTC()
	to := time.Now().AddDate(0, 0, 1).UTC()
	blocks, err := a.deps.Blocks.ListBetween(a.ctx, from, to)
	if err != nil {
		return 0, err
	}
	tagID := r.TagID
	count := 0
	for _, b := range blocks {
		if b.TagID != nil { // never overwrite manual tags
			continue
		}
		if !compiled[0].Match(b.ProcessName, b.WindowTitle) {
			continue
		}
		if err := a.deps.Blocks.SetTag(a.ctx, b.ID, &tagID, true); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

// ----- Tracker control ----------------------------------------------------

// PauseTracking stops the polling loop and closes the current block.
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

// StartManualTag opens a placeholder focus block tagged with the given tag,
// using "now" as the start time. If a manual block is already open, it is
// closed first so the new block records only the time after the click —
// switching tags in the tray submenu produces a clean handover. The tag is
// also pushed to the tracker so any program blocks it opens for the
// duration of the manual block inherit the same tag instead of being run
// through the auto-tag rule engine. Process tracking and auto-tagging stay
// live alongside manual mode whenever tracking is enabled — manual mode
// just overrides the tagging decision, it does not stop polling.
func (a *App) StartManualTag(tagID int64) error {
	if a.deps.Blocks == nil {
		return errors.New("blocks repository not configured")
	}
	if tagID <= 0 {
		return fmt.Errorf("invalid tag id: %d", tagID)
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	now := time.Now().UTC()

	// Close any in-progress manual block first so its end time is the moment
	// of the new click — not whatever close-time we'd compute on shutdown.
	if a.manualBlockID != nil {
		if err := a.deps.Blocks.Close(a.ctx, *a.manualBlockID, now); err != nil {
			a.logger.Warn("manual tag: close previous failed",
				"id", *a.manualBlockID, "err", err)
		}
		a.manualBlockID = nil
	}
	if a.deps.Tracker != nil {
		a.deps.Tracker.SetManualTag(&tagID)
	}

	id := tagID
	block := &storage.FocusBlock{
		ProcessName:   "",
		WindowTitle:   "",
		StartTime:     now,
		TagID:         &id,
		IsPlaceholder: true,
	}
	if err := a.deps.Blocks.Open(a.ctx, block); err != nil {
		return fmt.Errorf("open manual block: %w", err)
	}
	a.manualBlockID = &block.ID
	a.logger.Info("manual tag started", "block_id", block.ID, "tag_id", tagID)
	return nil
}

// StopManualTag closes the currently open manual block (if any) and clears
// the tracker's manual-tag override so future program blocks fall back to
// the auto-tag rule engine. Pause state is independent and left untouched.
func (a *App) StopManualTag() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.manualBlockID == nil {
		return nil
	}
	id := *a.manualBlockID
	now := time.Now().UTC()
	if err := a.deps.Blocks.Close(a.ctx, id, now); err != nil {
		return fmt.Errorf("close manual block: %w", err)
	}
	a.manualBlockID = nil
	if a.deps.Tracker != nil {
		a.deps.Tracker.SetManualTag(nil)
	}
	a.logger.Info("manual tag stopped", "block_id", id)
	return nil
}

// IsManualTagActive reports whether a manual tag block is currently open and,
// if so, the id of the tag it carries. Used by the tray to highlight the
// active entry in the manual-tag submenu.
func (a *App) IsManualTagActive() (int64, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.manualBlockID == nil {
		return 0, false
	}
	// We don't keep the tag id in App state; the only consumer needs the
	// boolean and can refresh tag info via ListTags. Return the block id so
	// callers can correlate if needed.
	return *a.manualBlockID, true
}

// ----- Settings -----------------------------------------------------------

// GetConfig returns the current config (Personio session secrets are not
// part of this struct).
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

// SaveConfig validates and persists a new config. The runtime adopts the new
// values via the OnConfigSet callback supplied at construction time.
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

// PersonioSessionStatus reports whether a captured session exists and what
// its tenant is. Valid is filled by PersonioCheck (a cheap network probe);
// PersonioStatus only inspects the persisted blob and leaves Valid==false.
type PersonioSessionStatus struct {
	HasSession bool      `json:"has_session"`
	Tenant     string    `json:"tenant"`
	EmployeeID int64     `json:"employee_id"`
	CapturedAt time.Time `json:"captured_at"`
	// Valid reflects the result of the most recent network probe — true
	// when the cookies still authenticate against Personio. Always false
	// from PersonioStatus(); true/false from PersonioCheck() depending on
	// the probe outcome.
	Valid bool `json:"valid"`
	// CheckedAt is when Valid was last produced. Zero when Valid is unset.
	CheckedAt time.Time `json:"checked_at,omitempty"`
	// Reason is a short human-readable hint shown in the status badge when
	// the session is missing or invalid (e.g. "kein Tenant", "Session
	// abgelaufen"). Empty when Valid is true.
	Reason string `json:"reason,omitempty"`
}

// PersonioStatus returns the current login state without hitting the network.
func (a *App) PersonioStatus() PersonioSessionStatus {
	if a.deps.Sessions == nil {
		a.logger.Debug("app: PersonioStatus — no session store wired")
		return PersonioSessionStatus{Reason: "kein Session-Speicher"}
	}
	s, err := a.deps.Sessions.Get()
	if err != nil || s == nil {
		a.logger.Debug("app: PersonioStatus — no stored session", "err", err)
		return PersonioSessionStatus{Reason: "nicht angemeldet"}
	}
	a.logger.Debug("app: PersonioStatus",
		"tenant", s.Tenant, "employee_id", s.EmployeeID, "captured_at", s.CapturedAt)
	return PersonioSessionStatus{
		HasSession: true,
		Tenant:     s.Tenant,
		EmployeeID: s.EmployeeID,
		CapturedAt: s.CapturedAt,
	}
}

// PersonioCheck probes the Personio app root with the stored cookies and
// reports whether the session still authenticates. The result populates the
// Valid / CheckedAt / Reason fields in addition to the metadata returned by
// PersonioStatus(), so the UI badge can colour-code itself.
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

// PersonioLogin launches an interactive Chrome session for the user to log
// into Personio, then captures and persists the resulting session cookies.
// Returns once login is complete (or an error on timeout/cancel).
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
		a.logger.Warn("app: PersonioLogin failed", "tenant", tenant, "err", err)
		return fmt.Errorf("personio login: %w", err)
	}

	a.logger.Info("app: PersonioLogin captured cookies",
		"tenant", res.Session.Tenant,
		"app_host", res.Session.AppHost,
		"cookie_count", len(res.Session.Cookies))

	if err := personio.Validate(a.ctx, res.Session); err != nil {
		a.logger.Warn("app: PersonioLogin validation failed", "err", err)
		return fmt.Errorf("validierung der erfassten Session fehlgeschlagen: %w", err)
	}

	// Resolve employee id once so subsequent sync calls don't need to.
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
		a.logger.Warn("app: SyncDay parse failed", "input", dayRFC3339, "err", err)
		return nil, fmt.Errorf("parse day: %w", err)
	}
	a.logger.Info("app: SyncDay", "day", day.Format("2006-01-02"))
	syncer, err := a.currentSyncer()
	if err != nil {
		a.logger.Warn("app: SyncDay no syncer", "err", err)
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

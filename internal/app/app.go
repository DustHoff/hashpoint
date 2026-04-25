// Package app exposes Wails-bound methods to the frontend. The App struct
// is the single bridge between the JS layer and the Go backend; no other
// package speaks to Wails directly.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/onesi/hashpoint/internal/personio"
	"github.com/onesi/hashpoint/internal/storage"
	"github.com/onesi/hashpoint/internal/tagging"
	"github.com/onesi/hashpoint/internal/tracker"
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
	Syncer   *personio.Syncer
	Version  VersionInfo
	Logger   *slog.Logger
}

// App is the Wails-bound facade. Methods on *App must be safe to call from
// the JS layer concurrently.
type App struct {
	ctx    context.Context
	deps   Deps
	logger *slog.Logger
}

// New constructs the app from its dependencies.
func New(deps Deps) *App {
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	return &App{deps: deps, logger: deps.Logger}
}

// Startup is invoked by Wails once the runtime is ready.
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	a.logger.Info("frontend started")
}

// Shutdown is invoked by Wails on window close. Tracker shutdown is handled
// in main; nothing to do here.
func (a *App) Shutdown(_ context.Context) {}

// Version returns build metadata for the "About" dialog.
func (a *App) Version() VersionInfo { return a.deps.Version }

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

// DeleteBlock removes a block.
func (a *App) DeleteBlock(id int64) error {
	return a.deps.Blocks.Delete(a.ctx, id)
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

// ----- Personio -----------------------------------------------------------

// SyncDay triggers a sync of the given UTC day.
func (a *App) SyncDay(dayRFC3339 string) (*personio.Result, error) {
	if a.deps.Syncer == nil {
		return nil, errors.New("personio not configured")
	}
	day, err := time.Parse(time.RFC3339, dayRFC3339)
	if err != nil {
		return nil, fmt.Errorf("parse day: %w", err)
	}
	return a.deps.Syncer.SyncDay(a.ctx, day.UTC())
}

// SyncRange triggers a sync of the given range.
func (a *App) SyncRange(fromRFC3339, toRFC3339 string) (*personio.Result, error) {
	if a.deps.Syncer == nil {
		return nil, errors.New("personio not configured")
	}
	from, err := time.Parse(time.RFC3339, fromRFC3339)
	if err != nil {
		return nil, err
	}
	to, err := time.Parse(time.RFC3339, toRFC3339)
	if err != nil {
		return nil, err
	}
	return a.deps.Syncer.SyncRange(a.ctx, from.UTC(), to.UTC())
}

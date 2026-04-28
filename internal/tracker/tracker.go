// Package tracker implements the foreground-window polling loop, idle
// detection, focus-block lifecycle and crash recovery.
//
// The loop runs in exactly one goroutine started by Run. It owns the current
// open block; all state mutation happens from this goroutine, so no mutex is
// needed on the in-memory state.
//
// Restrictions per CLAUDE.md §2: tracker must not import internal/app or
// internal/personio. It depends only on storage, tagging and winapi.
package tracker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/onesi/hashpoint/internal/storage"
	"github.com/onesi/hashpoint/internal/tagging"
	"github.com/onesi/hashpoint/internal/winapi"
)

// FocusSource produces the currently focused window and idle duration.
// Abstracted so tests can supply deterministic stubs.
type FocusSource interface {
	Foreground() (winapi.FocusInfo, error)
	IdleDuration() (time.Duration, error)
}

// Clock returns the current time. Indirection for tests.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now().UTC() }

// realFocusSource delegates to package winapi.
type realFocusSource struct{}

func (realFocusSource) Foreground() (winapi.FocusInfo, error) { return winapi.Foreground() }
func (realFocusSource) IdleDuration() (time.Duration, error)  { return winapi.IdleDuration() }

// Config controls runtime behavior of the tracker.
type Config struct {
	PollInterval  time.Duration
	IdleThreshold time.Duration
	// TagBlockGranularity snaps every focus block's start_time and end_time
	// to a fixed local-time grid of this width (start floored, end ceiled).
	// Adjacent blocks meet on a grid boundary so the §2.1 non-overlap
	// invariant is preserved. 0 disables snapping (the tracker stores raw
	// poll-clock timestamps as before).
	TagBlockGranularity time.Duration
}

// Tracker owns the focus-tracking lifecycle.
type Tracker struct {
	cfg    Config
	source FocusSource
	clock  Clock
	blocks storage.FocusBlockRepository
	rules  storage.RuleRepository
	logger *slog.Logger

	mu sync.Mutex
	// current holds the tracker's own open program-focus block (nil while
	// nothing is being tracked). Manual blocks live in the App layer and are
	// not reflected here.
	current *storage.FocusBlock
	// paused is the user-facing pause state controlled by Pause/Resume —
	// driven by tracking.enabled, the tray toggle and the timeline button.
	// The whole state machine collapses to this single flag: paused = no
	// process tracking, no auto-tagging, manual placeholder blocks still
	// allowed at the App layer; running = process tracking + auto-tag rules
	// active alongside any manual placeholder the user has open.
	paused bool
	// manualTagID is the tag id of the App's currently open manual block
	// (nil when no manual is active). When set, applyAutoTag inherits this
	// tag onto every new program block instead of running the rule engine —
	// so the user's manual tag wins over auto-tag rules without us having to
	// stop polling. Manual mode and Pause are deliberately independent.
	manualTagID *int64
	// blockGranularity mirrors Config.TagBlockGranularity but is mutable so
	// SetTagBlockGranularity can hot-reload changes from Settings without a
	// tracker restart. Guarded by mu — read by the tick goroutine, written
	// from the App layer.
	blockGranularity time.Duration
}

// Option is a functional option.
type Option func(*Tracker)

// WithFocusSource overrides the default winapi-backed source (for tests).
func WithFocusSource(s FocusSource) Option { return func(t *Tracker) { t.source = s } }

// WithClock overrides the default clock (for tests).
func WithClock(c Clock) Option { return func(t *Tracker) { t.clock = c } }

// New constructs a tracker. Run starts the loop.
func New(cfg Config, blocks storage.FocusBlockRepository, rules storage.RuleRepository, logger *slog.Logger, opts ...Option) *Tracker {
	if logger == nil {
		logger = slog.Default()
	}
	t := &Tracker{
		cfg:              cfg,
		source:           realFocusSource{},
		clock:            realClock{},
		blocks:           blocks,
		rules:            rules,
		logger:           logger,
		blockGranularity: cfg.TagBlockGranularity,
	}
	for _, o := range opts {
		o(t)
	}
	return t
}

// Pause stops opening new blocks; the currently open block (if any) is closed
// at the time Pause is called.
func (t *Tracker) Pause(ctx context.Context) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.paused = true
	if t.current != nil {
		end := t.snapEndAt(t.current.StartTime, t.clock.Now())
		_ = t.blocks.Close(ctx, t.current.ID, end)
		t.current = nil
	}
}

// Resume re-enables block opening.
func (t *Tracker) Resume() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.paused = false
}

// Paused reports the current pause state.
func (t *Tracker) Paused() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.paused
}

// SetTagBlockGranularity hot-reloads the grid step used to snap focus-block
// start/end times. 0 disables snapping. Negative values are coerced to 0.
// Already-open blocks keep the granularity that was active when they opened —
// the new value affects the next close + open boundary.
func (t *Tracker) SetTagBlockGranularity(d time.Duration) {
	if d < 0 {
		d = 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.blockGranularity = d
}

// snapEndAt returns the rounded end_time the tracker should write when
// closing a block opened at `start` while the wall clock reads `now`. With
// granularity disabled it returns now. Otherwise it returns the smallest
// grid boundary t >= now such that t - start is a positive multiple of the
// granularity step — a started X-min slot counts as a full slot. Callers
// must hold t.mu.
func (t *Tracker) snapEndAt(start, now time.Time) time.Time {
	step := t.blockGranularity
	if step <= 0 {
		return now
	}
	d := now.Sub(start)
	if d <= 0 {
		return start.Add(step)
	}
	slots := d / step
	if d%step != 0 {
		slots++
	}
	if slots == 0 {
		slots = 1
	}
	return start.Add(slots * step)
}

// snapStartAt returns the rounded start_time for a fresh block opened at
// wall-clock `now`. With granularity disabled it returns now. Otherwise it
// returns the latest grid boundary <= now ("floor"), bumped forward to
// `notBefore` if the previous block's rounded end already overlaps that
// floor. Callers must hold t.mu.
func (t *Tracker) snapStartAt(now, notBefore time.Time) time.Time {
	step := t.blockGranularity
	if step <= 0 {
		return now
	}
	floored := floorToStep(now, step)
	if !notBefore.IsZero() && notBefore.After(floored) {
		return notBefore
	}
	return floored
}

// floorToStep returns the largest grid boundary <= ts. The grid is anchored
// at local midnight so 15-min slots line up with :00/:15/:30/:45 boundaries
// the user sees, even in timezones whose UTC offset is not a whole hour.
func floorToStep(ts time.Time, step time.Duration) time.Time {
	if step <= 0 {
		return ts
	}
	local := ts.Local()
	midnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
	delta := local.Sub(midnight)
	return midnight.Add(delta - (delta % step))
}

// SetManualTag tells the tracker which tag the App's currently open manual
// block carries (nil when no manual block is open). While set, every new
// program block opened by the tracker inherits this tag instead of being
// matched against the auto-tag rule engine — manual selection wins over
// rules. The tracker keeps polling and recording window context the whole
// time; only the tagging decision changes.
func (t *Tracker) SetManualTag(tagID *int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if tagID == nil {
		t.manualTagID = nil
		return
	}
	v := *tagID
	t.manualTagID = &v
}

// Run starts the polling loop and blocks until ctx is cancelled. The current
// block is closed on shutdown.
func (t *Tracker) Run(ctx context.Context) error {
	if err := t.recover(ctx); err != nil {
		t.logger.Warn("crash recovery failed", "err", err)
	}

	ticker := time.NewTicker(t.cfg.PollInterval)
	defer ticker.Stop()

	t.logger.Info("tracker started",
		"poll_interval", t.cfg.PollInterval,
		"idle_threshold", t.cfg.IdleThreshold,
	)

	for {
		select {
		case <-ctx.Done():
			t.shutdown(context.Background())
			t.logger.Info("tracker stopped")
			return ctx.Err()
		case <-ticker.C:
			t.tick(ctx)
		}
	}
}

func (t *Tracker) shutdown(ctx context.Context) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.current != nil {
		end := t.snapEndAt(t.current.StartTime, t.clock.Now())
		if err := t.blocks.Close(ctx, t.current.ID, end); err != nil {
			t.logger.Warn("close on shutdown failed", "err", err)
		}
		t.current = nil
	}
}

// recover finalizes every block left open by a previous crash. Closing only
// the latest open (LastOpen) leaves earlier opens overlapping with anything
// the user records after the relaunch — Personio rejects overlapping work
// periods, so each leftover open is closed at min(start+idleThreshold, now)
// and at the earliest the next block's start to avoid reintroducing overlap.
func (t *Tracker) recover(ctx context.Context) error {
	opens, err := t.blocks.ListOpen(ctx)
	if err != nil {
		return fmt.Errorf("list open blocks: %w", err)
	}
	if len(opens) == 0 {
		return nil
	}
	now := t.clock.Now()
	for i, open := range opens {
		end := open.StartTime.Add(t.cfg.IdleThreshold)
		if end.After(now) {
			end = now
		}
		// If a later open block exists, this one must end no later than the
		// next one's start — otherwise closing it would create an overlap
		// that the storage layer would refuse.
		if i+1 < len(opens) && opens[i+1].StartTime.Before(end) {
			end = opens[i+1].StartTime
		}
		// Snap to the granularity grid so recovered blocks share the same
		// quantization as freshly tracked ones — but never past the next
		// open's start (would re-introduce overlap).
		t.mu.Lock()
		snapped := t.snapEndAt(open.StartTime, end)
		t.mu.Unlock()
		if i+1 < len(opens) && opens[i+1].StartTime.Before(snapped) {
			snapped = opens[i+1].StartTime
		}
		end = snapped
		t.logger.Info("recovering open block from previous run",
			"id", open.ID, "process", open.ProcessName,
			"start", open.StartTime.Format(time.RFC3339),
			"recovered_end", end.Format(time.RFC3339),
		)
		if err := t.blocks.Close(ctx, open.ID, end); err != nil {
			return fmt.Errorf("close open block %d: %w", open.ID, err)
		}
	}
	return nil
}

func (t *Tracker) tick(ctx context.Context) {
	if t.Paused() {
		return
	}
	idle, err := t.source.IdleDuration()
	if err != nil && !errors.Is(err, winapi.ErrUnsupported) {
		t.logger.Debug("idle query failed", "err", err)
	}
	if idle >= t.cfg.IdleThreshold {
		t.handleIdle(ctx)
		return
	}
	info, err := t.source.Foreground()
	if err != nil && !errors.Is(err, winapi.ErrUnsupported) {
		t.logger.Debug("foreground query failed", "err", err)
		return
	}
	if info.IsZero() {
		// E.g. lock screen — close any open block.
		t.closeCurrent(ctx)
		return
	}
	t.handleFocus(ctx, info)
}

func (t *Tracker) handleIdle(ctx context.Context) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.current == nil {
		return
	}
	end := t.snapEndAt(t.current.StartTime, t.clock.Now())
	if err := t.blocks.MarkIdle(ctx, t.current.ID, end); err != nil {
		t.logger.Warn("mark idle failed", "err", err)
		return
	}
	t.logger.Debug("block marked idle", "id", t.current.ID)
	t.current = nil
}

func (t *Tracker) closeCurrent(ctx context.Context) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.current == nil {
		return
	}
	end := t.snapEndAt(t.current.StartTime, t.clock.Now())
	if err := t.blocks.Close(ctx, t.current.ID, end); err != nil {
		t.logger.Warn("close failed", "err", err)
	}
	t.current = nil
}

func (t *Tracker) handleFocus(ctx context.Context, info winapi.FocusInfo) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Same focus → keep current block open.
	if t.current != nil &&
		t.current.ProcessName == info.ProcessName &&
		t.current.WindowTitle == info.Title {
		return
	}

	now := t.clock.Now()
	var prevEnd time.Time
	if t.current != nil {
		prevEnd = t.snapEndAt(t.current.StartTime, now)
		if err := t.blocks.Close(ctx, t.current.ID, prevEnd); err != nil {
			t.logger.Warn("close on switch failed", "err", err)
		}
	}

	b := &storage.FocusBlock{
		ProcessName: info.ProcessName,
		ProcessPath: info.ProcessPath,
		WindowTitle: info.Title,
		StartTime:   t.snapStartAt(now, prevEnd),
	}
	if err := t.blocks.Open(ctx, b); err != nil {
		t.logger.Warn("open block failed", "err", err)
		t.current = nil
		return
	}
	t.applyAutoTag(ctx, b)
	t.current = b

	// Title is debug-only by spec §5: never log on info+.
	t.logger.Debug("opened block",
		"id", b.ID, "process", b.ProcessName, "title", b.WindowTitle)
}

// applyAutoTag tags the freshly opened block. Caller holds t.mu.
//
// If the App has a manual-tag block open, every new program block inherits
// that tag instead of being matched against the rule engine — the user's
// explicit choice wins over auto-tag rules without us having to stop polling.
// auto_tagged is left false in that case so the timeline can still tell
// rule-applied tags from manually-driven ones.
func (t *Tracker) applyAutoTag(ctx context.Context, b *storage.FocusBlock) {
	if t.manualTagID != nil {
		tagID := *t.manualTagID
		b.TagID = &tagID
		if err := t.blocks.SetTag(ctx, b.ID, &tagID, false); err != nil {
			t.logger.Warn("manual tag inheritance failed", "err", err)
		}
		return
	}
	rules, err := t.rules.ListEnabled(ctx)
	if err != nil {
		t.logger.Debug("auto-tag: list rules failed", "err", err)
		return
	}
	if len(rules) == 0 {
		return
	}
	compiled, err := tagging.Compile(rules)
	if err != nil {
		t.logger.Warn("auto-tag: compile rules failed", "err", err)
		return
	}
	if hit := tagging.FirstMatch(compiled, b.ProcessName, b.WindowTitle); hit != nil {
		tagID := hit.Rule.TagID
		b.TagID = &tagID
		b.AutoTagged = true
		if err := t.blocks.SetTag(ctx, b.ID, &tagID, true); err != nil {
			t.logger.Warn("auto-tag: set tag failed", "err", err)
		}
	}
}

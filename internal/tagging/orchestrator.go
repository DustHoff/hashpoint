package tagging

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/onesi/hashpoint/internal/storage"
)

// Orchestrator owns the tag_blocks table. It listens to focus-change events
// from the tracker and mediates the lifecycle of auto-tag and manual-tag
// blocks. No other component writes to tag_blocks directly.
//
// State machine in brief:
//
//   - Auto-tag-blocks open when the focused process matches a rule and stay
//     open as long as a matching process keeps focus. They close (snapped to
//     granularity floor) when the matching focus ends. Sub-granularity
//     matches produce no block at all (zero-length suppression).
//
//   - At most one open-ended manual tag block exists. While open it acts as
//     the default tagging context: a process matching an auto-tag rule
//     interrupts it (manual is paused, auto runs); when the auto closes the
//     manual resumes with the same tag and description.
//
//   - On idle / focus-cleared the open manual is paused (closed) the same
//     way an auto-tag interrupts it; it resumes on the next focused process
//     (provided that process doesn't itself match a rule).
//
//   - A manual range tag (drag-to-tag on the timeline) wins over any
//     auto-tag block it overlaps — auto blocks are trimmed, split, or
//     deleted as needed. It is rejected if it overlaps an existing manual
//     block.
type Orchestrator struct {
	blocks storage.TagBlockRepository
	tracks storage.ProcessTrackRepository
	rules  storage.RuleRepository
	logger *slog.Logger
	clock  func() time.Time

	mu             sync.Mutex
	granularity    time.Duration
	focusActive    bool
	focusedProcess focusInfo

	openAuto     *autoState
	openManual   *manualState
	pausedManual *pausedManualState
}

type focusInfo struct{ name, title string }

type autoState struct {
	blockID int64
	ruleID  int64
	tagID   int64
}

type manualState struct {
	blockID     int64
	tagID       int64
	description string
}

type pausedManualState struct {
	tagID       int64
	description string
}

// NewOrchestrator constructs an Orchestrator.
func NewOrchestrator(
	blocks storage.TagBlockRepository,
	tracks storage.ProcessTrackRepository,
	rules storage.RuleRepository,
	logger *slog.Logger,
) *Orchestrator {
	if logger == nil {
		logger = slog.Default()
	}
	return &Orchestrator{
		blocks: blocks,
		tracks: tracks,
		rules:  rules,
		logger: logger,
		clock:  func() time.Time { return time.Now().UTC() },
	}
}

// SetClock overrides the wall-clock source (for tests).
func (o *Orchestrator) SetClock(f func() time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.clock = f
}

// SetGranularity hot-reloads the grid step. 0 disables snapping.
func (o *Orchestrator) SetGranularity(d time.Duration) {
	if d < 0 {
		d = 0
	}
	o.mu.Lock()
	o.granularity = d
	o.mu.Unlock()
}

// Recover closes every tag block left open by a previous crash. Manual
// blocks are closed at the last process-track end (or `now` when no tracking
// data exists); auto blocks are closed at the same instant — the
// orchestrator restarts cold and lets fresh focus events drive future state.
func (o *Orchestrator) Recover(ctx context.Context) error {
	opens, err := o.blocks.ListOpen(ctx)
	if err != nil {
		return fmt.Errorf("list open tag blocks: %w", err)
	}
	if len(opens) == 0 {
		return nil
	}
	now := o.clock()
	fallback, err := o.tracks.LastEnd(ctx)
	if err != nil {
		o.logger.Warn("recover: read last process-track end failed", "err", err)
	}
	if fallback.IsZero() {
		fallback = now
	}
	for _, b := range opens {
		end := o.snapFloor(fallback)
		if !end.After(b.StartTime) {
			if err := o.blocks.Delete(ctx, b.ID); err != nil {
				o.logger.Warn("recover: delete zero-length open block failed", "id", b.ID, "err", err)
			}
			continue
		}
		if err := o.blocks.SetEnd(ctx, b.ID, end); err != nil {
			o.logger.Warn("recover: close open tag block failed", "id", b.ID, "err", err)
		}
	}
	return nil
}

// OnFocusChanged is called by the tracker on each focus change.
func (o *Orchestrator) OnFocusChanged(ctx context.Context, name, title string, at time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.focusActive = true
	o.focusedProcess = focusInfo{name: name, title: title}
	o.advance(ctx, at, o.matchRule(ctx, name, title))
}

// OnFocusCleared is called by the tracker on idle / lock screen / shutdown.
func (o *Orchestrator) OnFocusCleared(ctx context.Context, at time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.focusActive = false
	o.focusedProcess = focusInfo{}
	o.advance(ctx, at, nil)
}

// advance is the core state-machine step. `rule` is the rule that should
// drive auto-tag state at `at` — nil when no rule applies (or focus is
// cleared). The method holds o.mu.
func (o *Orchestrator) advance(ctx context.Context, at time.Time, rule *storage.Rule) {
	snap := o.snapFloor(at)

	if o.openAuto != nil {
		if rule != nil && rule.ID == o.openAuto.ruleID {
			return
		}
		o.closeAuto(ctx, snap)
	}

	if rule != nil {
		if o.openManual != nil {
			o.pauseManual(ctx, snap)
		}
		o.startAuto(ctx, *rule, snap)
		return
	}

	if !o.focusActive && o.openManual != nil {
		o.pauseManual(ctx, snap)
		return
	}

	if o.pausedManual != nil && o.openManual == nil && o.focusActive {
		o.resumeManual(ctx, snap)
	}
}

// StartManualOpenEnded creates an open-ended manual tag, or schedules one
// (pausedManual) when an auto-tag is currently active.
func (o *Orchestrator) StartManualOpenEnded(ctx context.Context, tagID int64, description string) error {
	if tagID <= 0 {
		return fmt.Errorf("invalid tag id: %d", tagID)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	snap := o.snapFloor(o.clock())

	if o.openManual != nil {
		o.closeManual(ctx, snap)
	}
	o.pausedManual = nil

	if o.openAuto != nil {
		o.pausedManual = &pausedManualState{tagID: tagID, description: strings.TrimSpace(description)}
		return nil
	}
	return o.startManual(ctx, tagID, description, snap)
}

// StopManualOpenEnded closes the open or paused manual tag.
func (o *Orchestrator) StopManualOpenEnded(ctx context.Context) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	snap := o.snapFloor(o.clock())
	if o.openManual != nil {
		o.closeManual(ctx, snap)
	}
	o.pausedManual = nil
	return nil
}

// CloseDanglingManualAtStartup closes any manual tag block left open by a
// previous run. Called once during App startup, before the tracker starts
// firing events. Tracking-disabled callers pass a wall-clock fallback for
// cases where no process-track exists.
func (o *Orchestrator) CloseDanglingManualAtStartup(ctx context.Context, fallback time.Time) error {
	open, err := o.blocks.ListOpenManual(ctx)
	if err != nil {
		return fmt.Errorf("list open manual: %w", err)
	}
	if len(open) == 0 {
		return nil
	}
	target := fallback
	if last, err := o.tracks.LastEnd(ctx); err == nil && !last.IsZero() {
		target = last
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	end := o.snapFloor(target)
	for _, b := range open {
		if !end.After(b.StartTime) {
			if err := o.blocks.Delete(ctx, b.ID); err != nil {
				o.logger.Warn("startup: delete zero-length manual failed", "id", b.ID, "err", err)
			}
			continue
		}
		if err := o.blocks.SetEnd(ctx, b.ID, end); err != nil {
			o.logger.Warn("startup: close dangling manual failed", "id", b.ID, "err", err)
		}
	}
	return nil
}

// IsManualActive reports whether an open-ended manual tag is currently in
// progress (open or paused).
func (o *Orchestrator) IsManualActive() (tagID int64, active bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.openManual != nil {
		return o.openManual.tagID, true
	}
	if o.pausedManual != nil {
		return o.pausedManual.tagID, true
	}
	return 0, false
}

// CreateManualRange inserts a manual tag block covering [from, to). Snaps
// to granularity (start floor, end ceil). Trims/splits/deletes overlapping
// auto-tag blocks. Rejects overlap with existing manual blocks.
func (o *Orchestrator) CreateManualRange(ctx context.Context, tagID int64, description string, from, to time.Time) error {
	if tagID <= 0 {
		return fmt.Errorf("invalid tag id: %d", tagID)
	}
	if !to.After(from) {
		return errors.New("from must be before to")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	from = o.snapFloor(from)
	to = o.snapCeil(to)
	if !to.After(from) {
		return errors.New("snapped range is empty")
	}

	overlapping, err := o.blocks.ListOverlapping(ctx, from, to)
	if err != nil {
		return fmt.Errorf("list overlapping: %w", err)
	}
	for _, b := range overlapping {
		if b.IsManual {
			return fmt.Errorf("%w: existing manual tag block id=%d", storage.ErrOverlap, b.ID)
		}
	}
	for _, b := range overlapping {
		if err := o.carveAuto(ctx, b, from, to); err != nil {
			return fmt.Errorf("carve auto block %d: %w", b.ID, err)
		}
	}
	o.refreshAutoState(ctx)

	desc := strings.TrimSpace(description)
	var dptr *string
	if desc != "" {
		dptr = &desc
	}
	end := to
	block := &storage.TagBlock{
		TagID:       tagID,
		Description: dptr,
		StartTime:   from,
		EndTime:     &end,
		DurationSec: int64(end.Sub(from).Round(time.Second).Seconds()),
		IsManual:    true,
	}
	if err := o.blocks.Open(ctx, block); err != nil {
		return fmt.Errorf("open manual range: %w", err)
	}
	return nil
}

// carveAuto trims an auto-tag block to make room for a manual range that
// overlaps it. Cases:
//  1. b ⊆ [from, to)        → delete b
//  2. b crosses from         → SetEnd(b, from)
//  3. b crosses to           → SetStart(b, to)
//  4. b ⊃ [from, to)         → SetEnd(b, from); insert right half
func (o *Orchestrator) carveAuto(ctx context.Context, b storage.TagBlock, from, to time.Time) error {
	bstart := b.StartTime
	var bend time.Time
	if b.EndTime != nil {
		bend = *b.EndTime
	}
	endsBeforeOrAtTo := b.EndTime != nil && !bend.After(to)
	startsAfterOrAtFrom := !bstart.Before(from)

	if startsAfterOrAtFrom && endsBeforeOrAtTo {
		if o.openAuto != nil && o.openAuto.blockID == b.ID {
			o.openAuto = nil
		}
		return o.blocks.Delete(ctx, b.ID)
	}
	if bstart.Before(from) && (b.EndTime == nil || bend.After(to)) {
		if err := o.blocks.SetEnd(ctx, b.ID, from); err != nil {
			return err
		}
		var rightEnd *time.Time
		var dur int64
		if b.EndTime != nil {
			re := bend
			rightEnd = &re
			dur = int64(re.Sub(to).Round(time.Second).Seconds())
		}
		right := &storage.TagBlock{
			TagID:       b.TagID,
			Description: b.Description,
			StartTime:   to,
			EndTime:     rightEnd,
			DurationSec: dur,
			IsManual:    b.IsManual,
		}
		if err := o.blocks.Open(ctx, right); err != nil {
			return err
		}
		if o.openAuto != nil && o.openAuto.blockID == b.ID {
			o.openAuto = nil
		}
		return nil
	}
	if bstart.Before(from) {
		return o.blocks.SetEnd(ctx, b.ID, from)
	}
	return o.blocks.SetStart(ctx, b.ID, to)
}

func (o *Orchestrator) refreshAutoState(ctx context.Context) {
	if o.openAuto == nil {
		return
	}
	b, err := o.blocks.Get(ctx, o.openAuto.blockID)
	if err != nil || b == nil || b.EndTime != nil {
		o.openAuto = nil
	}
}

func (o *Orchestrator) closeAuto(ctx context.Context, snappedEnd time.Time) {
	if o.openAuto == nil {
		return
	}
	state := o.openAuto
	b, err := o.blocks.Get(ctx, state.blockID)
	if err != nil || b == nil {
		o.logger.Warn("close auto: block missing", "id", state.blockID, "err", err)
		o.openAuto = nil
		return
	}
	if !snappedEnd.After(b.StartTime) {
		if err := o.blocks.Delete(ctx, state.blockID); err != nil {
			o.logger.Warn("close auto: delete zero-length failed", "id", state.blockID, "err", err)
		}
		o.openAuto = nil
		return
	}
	if err := o.blocks.Close(ctx, state.blockID, snappedEnd); err != nil {
		o.logger.Warn("close auto failed", "id", state.blockID, "err", err)
		o.openAuto = nil
		return
	}
	o.openAuto = nil
	if o.pausedManual != nil && o.focusActive {
		o.resumeManual(ctx, snappedEnd)
	}
}

func (o *Orchestrator) startAuto(ctx context.Context, rule storage.Rule, snappedStart time.Time) {
	block := &storage.TagBlock{
		TagID:     rule.TagID,
		StartTime: snappedStart,
		IsManual:  false,
	}
	if err := o.blocks.Open(ctx, block); err != nil {
		o.logger.Warn("open auto failed", "rule_id", rule.ID, "err", err)
		return
	}
	o.openAuto = &autoState{blockID: block.ID, ruleID: rule.ID, tagID: rule.TagID}
}

func (o *Orchestrator) startManual(ctx context.Context, tagID int64, description string, snappedStart time.Time) error {
	desc := strings.TrimSpace(description)
	var dptr *string
	if desc != "" {
		dptr = &desc
	}
	block := &storage.TagBlock{
		TagID:       tagID,
		Description: dptr,
		StartTime:   snappedStart,
		IsManual:    true,
	}
	if err := o.blocks.Open(ctx, block); err != nil {
		return fmt.Errorf("open manual: %w", err)
	}
	o.openManual = &manualState{blockID: block.ID, tagID: tagID, description: desc}
	return nil
}

func (o *Orchestrator) closeManual(ctx context.Context, snappedEnd time.Time) {
	if o.openManual == nil {
		return
	}
	m := o.openManual
	b, err := o.blocks.Get(ctx, m.blockID)
	if err != nil || b == nil {
		o.openManual = nil
		return
	}
	if !snappedEnd.After(b.StartTime) {
		if err := o.blocks.Delete(ctx, m.blockID); err != nil {
			o.logger.Warn("close manual: delete zero-length failed", "id", m.blockID, "err", err)
		}
		o.openManual = nil
		return
	}
	if err := o.blocks.Close(ctx, m.blockID, snappedEnd); err != nil {
		o.logger.Warn("close manual failed", "id", m.blockID, "err", err)
	}
	o.openManual = nil
}

func (o *Orchestrator) pauseManual(ctx context.Context, snappedEnd time.Time) {
	if o.openManual == nil {
		return
	}
	m := o.openManual
	o.pausedManual = &pausedManualState{tagID: m.tagID, description: m.description}
	o.closeManual(ctx, snappedEnd)
}

func (o *Orchestrator) resumeManual(ctx context.Context, snappedStart time.Time) {
	if o.pausedManual == nil {
		return
	}
	p := o.pausedManual
	if err := o.startManual(ctx, p.tagID, p.description, snappedStart); err != nil {
		o.logger.Warn("resume manual failed", "err", err)
		return
	}
	o.pausedManual = nil
}

// snapFloor returns the largest grid boundary <= t. Anchored at local
// midnight so 15-min slots align with :00/:15/:30/:45 the user sees.
func (o *Orchestrator) snapFloor(t time.Time) time.Time {
	if o.granularity <= 0 {
		return t
	}
	local := t.Local()
	midnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
	delta := local.Sub(midnight)
	rem := delta % o.granularity
	if rem == 0 {
		return t
	}
	return midnight.Add(delta - rem)
}

// snapCeil returns the smallest grid boundary >= t.
func (o *Orchestrator) snapCeil(t time.Time) time.Time {
	if o.granularity <= 0 {
		return t
	}
	local := t.Local()
	midnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
	delta := local.Sub(midnight)
	rem := delta % o.granularity
	if rem == 0 {
		return t
	}
	return midnight.Add(delta - rem + o.granularity)
}

func (o *Orchestrator) matchRule(ctx context.Context, name, title string) *storage.Rule {
	rules, err := o.rules.ListEnabled(ctx)
	if err != nil {
		o.logger.Debug("orchestrator: list rules failed", "err", err)
		return nil
	}
	if len(rules) == 0 {
		return nil
	}
	compiled, err := Compile(rules)
	if err != nil {
		o.logger.Warn("orchestrator: compile rules failed", "err", err)
		return nil
	}
	if hit := FirstMatch(compiled, name, title); hit != nil {
		r := hit.Rule
		return &r
	}
	return nil
}

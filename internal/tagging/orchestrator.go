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

// Reason codes recorded on every "tag block closed" log entry. Keep these
// stable — they are grepped from log files when diagnosing why a block
// transitioned from open to closed.
const (
	reasonAutoFocusLost             = "auto_focus_lost"
	reasonAutoFocusLostZero         = "auto_focus_lost_zero_length"
	reasonAutoRuleSwitched          = "auto_rule_switched"
	reasonAutoRuleSwitchedZero      = "auto_rule_switched_zero_length"
	reasonManualPausedForAuto       = "manual_paused_for_auto"
	reasonManualPausedForAutoZero   = "manual_paused_for_auto_zero_length"
	reasonManualPausedForIdle       = "manual_paused_for_idle"
	reasonManualPausedForIdleZero   = "manual_paused_for_idle_zero_length"
	reasonManualReplaced            = "manual_replaced"
	reasonManualReplacedZero        = "manual_replaced_zero_length"
	reasonManualStoppedByUser       = "manual_stopped_by_user"
	reasonManualStoppedByUserZero   = "manual_stopped_by_user_zero_length"
	reasonRangeCarveDelete          = "range_carve_delete"
	reasonRangeCarveLeft            = "range_carve_left"
	reasonRangeCarveRight           = "range_carve_right"
	reasonRecoverCrash              = "recover_crash"
	reasonRecoverCrashZero          = "recover_crash_zero_length"
	reasonStartupDanglingManual     = "startup_dangling_manual"
	reasonStartupDanglingManualZero = "startup_dangling_manual_zero_length"
)

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

// logBlockClosed emits a single structured Info entry whenever an open
// tag block transitions to a closed (or deleted-as-zero-length) state.
// The reason code disambiguates the call site; extra fields carry call-site
// specific context (rule id, paused-manual presence, etc). Caller holds o.mu.
func (o *Orchestrator) logBlockClosed(b storage.TagBlock, end time.Time, reason string, extra ...any) {
	dur := int64(0)
	if !end.Before(b.StartTime) {
		dur = int64(end.Sub(b.StartTime).Round(time.Second).Seconds())
	}
	args := []any{
		"id", b.ID,
		"tag_id", b.TagID,
		"is_manual", b.IsManual,
		"start", b.StartTime,
		"end", end,
		"duration_sec", dur,
		"reason", reason,
		"focus_active", o.focusActive,
		"focus_process", o.focusedProcess.name,
		"had_paused_manual", o.pausedManual != nil,
	}
	args = append(args, extra...)
	o.logger.Info("tag block closed", args...)
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
				continue
			}
			o.logBlockClosed(b, end, reasonRecoverCrashZero)
			continue
		}
		if err := o.blocks.SetEnd(ctx, b.ID, end); err != nil {
			o.logger.Warn("recover: close open tag block failed", "id", b.ID, "err", err)
			continue
		}
		o.logBlockClosed(b, end, reasonRecoverCrash)
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
		reason := reasonAutoFocusLost
		if rule != nil && rule.ID == o.openAuto.ruleID {
			return
		}
		if rule != nil {
			reason = reasonAutoRuleSwitched
		}
		o.closeAuto(ctx, snap, reason)
	}

	if rule != nil {
		if o.openManual != nil {
			o.pauseManual(ctx, snap, reasonManualPausedForAuto)
		}
		o.startAuto(ctx, *rule, snap)
		return
	}

	if !o.focusActive && o.openManual != nil {
		o.pauseManual(ctx, snap, reasonManualPausedForIdle)
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
		o.closeManual(ctx, snap, reasonManualReplaced)
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
		o.closeManual(ctx, snap, reasonManualStoppedByUser)
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
				continue
			}
			o.logBlockClosed(b, end, reasonStartupDanglingManualZero)
			continue
		}
		if err := o.blocks.SetEnd(ctx, b.ID, end); err != nil {
			o.logger.Warn("startup: close dangling manual failed", "id", b.ID, "err", err)
			continue
		}
		o.logBlockClosed(b, end, reasonStartupDanglingManual)
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
		if err := o.blocks.Delete(ctx, b.ID); err != nil {
			return err
		}
		closeAt := to
		if b.EndTime != nil {
			closeAt = bend
		}
		o.logBlockClosed(b, closeAt, reasonRangeCarveDelete,
			"was_open", b.EndTime == nil,
			"manual_range_from", from, "manual_range_to", to)
		return nil
	}
	if bstart.Before(from) && (b.EndTime == nil || bend.After(to)) {
		if err := o.blocks.SetEnd(ctx, b.ID, from); err != nil {
			return err
		}
		o.logBlockClosed(b, from, reasonRangeCarveLeft,
			"was_open", b.EndTime == nil,
			"manual_range_from", from, "manual_range_to", to)
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
		if err := o.blocks.SetEnd(ctx, b.ID, from); err != nil {
			return err
		}
		o.logBlockClosed(b, from, reasonRangeCarveLeft,
			"was_open", b.EndTime == nil,
			"manual_range_from", from, "manual_range_to", to)
		return nil
	}
	if err := o.blocks.SetStart(ctx, b.ID, to); err != nil {
		return err
	}
	o.logBlockClosed(b, to, reasonRangeCarveRight,
		"was_open", b.EndTime == nil,
		"manual_range_from", from, "manual_range_to", to)
	return nil
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

func (o *Orchestrator) closeAuto(ctx context.Context, snappedEnd time.Time, reason string) {
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
			o.openAuto = nil
			return
		}
		zeroReason := reasonAutoFocusLostZero
		if reason == reasonAutoRuleSwitched {
			zeroReason = reasonAutoRuleSwitchedZero
		}
		o.logBlockClosed(*b, snappedEnd, zeroReason, "rule_id", state.ruleID)
		o.openAuto = nil
		return
	}
	if err := o.blocks.Close(ctx, state.blockID, snappedEnd); err != nil {
		o.logger.Warn("close auto failed", "id", state.blockID, "err", err)
		o.openAuto = nil
		return
	}
	o.logBlockClosed(*b, snappedEnd, reason, "rule_id", state.ruleID)
	o.openAuto = nil
	if o.pausedManual != nil && o.focusActive {
		o.resumeManual(ctx, snappedEnd)
	}
}

func (o *Orchestrator) startAuto(ctx context.Context, rule storage.Rule, snappedStart time.Time) {
	var dptr *string
	if rule.Description != nil {
		if d := strings.TrimSpace(*rule.Description); d != "" {
			dptr = &d
		}
	}
	block := &storage.TagBlock{
		TagID:       rule.TagID,
		Description: dptr,
		StartTime:   snappedStart,
		IsManual:    false,
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

func (o *Orchestrator) closeManual(ctx context.Context, snappedEnd time.Time, reason string) {
	if o.openManual == nil {
		return
	}
	m := o.openManual
	b, err := o.blocks.Get(ctx, m.blockID)
	if err != nil || b == nil {
		o.logger.Warn("close manual: block missing", "id", m.blockID, "err", err)
		o.openManual = nil
		return
	}
	if !snappedEnd.After(b.StartTime) {
		if err := o.blocks.Delete(ctx, m.blockID); err != nil {
			o.logger.Warn("close manual: delete zero-length failed", "id", m.blockID, "err", err)
			o.openManual = nil
			return
		}
		o.logBlockClosed(*b, snappedEnd, manualZeroReason(reason))
		o.openManual = nil
		return
	}
	if err := o.blocks.Close(ctx, m.blockID, snappedEnd); err != nil {
		o.logger.Warn("close manual failed", "id", m.blockID, "err", err)
		o.openManual = nil
		return
	}
	o.logBlockClosed(*b, snappedEnd, reason)
	o.openManual = nil
}

func manualZeroReason(reason string) string {
	switch reason {
	case reasonManualPausedForAuto:
		return reasonManualPausedForAutoZero
	case reasonManualPausedForIdle:
		return reasonManualPausedForIdleZero
	case reasonManualReplaced:
		return reasonManualReplacedZero
	case reasonManualStoppedByUser:
		return reasonManualStoppedByUserZero
	}
	return reason
}

func (o *Orchestrator) pauseManual(ctx context.Context, snappedEnd time.Time, reason string) {
	if o.openManual == nil {
		return
	}
	m := o.openManual
	o.pausedManual = &pausedManualState{tagID: m.tagID, description: m.description}
	o.closeManual(ctx, snappedEnd, reason)
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

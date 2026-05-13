package tagging

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dusthoff/hashpoint/internal/storage"
	"github.com/dusthoff/hashpoint/internal/tracker"
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

	// openCommAuto is the auto-tag block driven by a communication-process
	// session (Teams, Zoom, …). While non-nil it overrides focus-driven
	// auto-tags: focus changes update focusedProcess but do NOT touch
	// openAuto. When openCommAuto closes, the orchestrator re-evaluates
	// the focused process to potentially open a regular openAuto or
	// resume a paused manual.
	openCommAuto *autoState

	// blockClosedHook fires after every block transitions from open to
	// closed (logBlockClosed). Used by the App layer to trigger oncall.
	// Recheck without coupling the orchestrator to the plugin system.
	// Dispatched in a goroutine so downstream I/O does not extend the
	// orchestrator's mu.Lock window. Set once via SetBlockClosedHook.
	hookMu          sync.RWMutex
	blockClosedHook BlockClosedHook

	// pluginResolver is the optional plugin-side auto-tag fallback.
	// Read under resolverMu separately from mu so the orchestrator can
	// drop the mu lock around a Resolve() call (the App layer's
	// implementation may touch storage). nil when no plugin host is
	// wired (or when the user has no autotag plugins installed).
	resolverMu     sync.RWMutex
	pluginResolver PluginAutoTagResolver
}

// BlockClosedHook is invoked once per block-close transition. The blockID
// is the only argument because callers (recheck) re-read the block's
// current state from the repo — passing a stale snapshot through the
// callback would race with subsequent edits.
type BlockClosedHook func(ctx context.Context, blockID int64)

type focusInfo struct{ name, title string }

// Reason codes recorded on every "tag block closed" log entry. Keep these
// stable — they are grepped from log files when diagnosing why a block
// transitioned from open to closed.
const (
	reasonAutoFocusLost             = "auto_focus_lost"
	reasonAutoFocusLostZero         = "auto_focus_lost_zero_length"
	reasonAutoRuleSwitched          = "auto_rule_switched"
	reasonAutoRuleSwitchedZero      = "auto_rule_switched_zero_length"
	reasonAutoOverriddenByComm      = "auto_overridden_by_comm"
	reasonAutoOverriddenByCommZero  = "auto_overridden_by_comm_zero_length"
	reasonManualPausedForAuto       = "manual_paused_for_auto"
	reasonManualPausedForAutoZero   = "manual_paused_for_auto_zero_length"
	reasonManualPausedForComm       = "manual_paused_for_comm"
	reasonManualPausedForCommZero   = "manual_paused_for_comm_zero_length"
	reasonManualPausedForIdle       = "manual_paused_for_idle"
	reasonManualPausedForIdleZero   = "manual_paused_for_idle_zero_length"
	reasonManualReplaced            = "manual_replaced"
	reasonManualReplacedZero        = "manual_replaced_zero_length"
	reasonManualStoppedByUser       = "manual_stopped_by_user"
	reasonManualStoppedByUserZero   = "manual_stopped_by_user_zero_length"
	reasonCommWindowGone            = "comm_window_gone"
	reasonCommWindowGoneZero        = "comm_window_gone_zero_length"
	reasonCommRuleSwitched          = "comm_rule_switched"
	reasonCommRuleSwitchedZero      = "comm_rule_switched_zero_length"
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
	// Exactly one of (ruleID > 0) or (pluginName != "") is set: ruleID
	// for a user-rule-driven auto-tag, pluginName for a plugin-resolver-
	// driven one. The two paths take the same on/off lifecycle but
	// surface different log fields and use different equality keys.
	ruleID     int64
	pluginName string
	tagID      int64
}

// sourceKey is the equality discriminator the advance loop uses to
// decide whether a fresh match constitutes a no-op (same source) or a
// switch (different source). Rule and plugin sources never collide
// because their key prefixes differ.
func (s autoState) sourceKey() string {
	if s.pluginName != "" {
		return "plugin:" + s.pluginName
	}
	return "rule:" + strconv.FormatInt(s.ruleID, 10)
}

// autoMatch carries the resolved auto-tag descriptor through the
// orchestrator's match → advance → startAuto pipeline. The lifecycle
// methods key off sourceKey() so a rule-to-plugin (or plugin-to-rule)
// transition closes the old block and opens a fresh one, while a same-
// source re-evaluation is a no-op.
type autoMatch struct {
	ruleID      int64
	pluginName  string
	tagID       int64
	description string
}

// sourceKey mirrors autoState.sourceKey so an open autoState can be
// compared against a candidate autoMatch without constructing a fake
// state.
func (m autoMatch) sourceKey() string {
	if m.pluginName != "" {
		return "plugin:" + m.pluginName
	}
	return "rule:" + strconv.FormatInt(m.ruleID, 10)
}

// PluginAutoTagMatch is the resolved-and-materialised auto-tag descriptor
// the App layer hands back from a PluginAutoTagResolver. The TagID has
// already been resolved (or auto-created) against the tags table so the
// orchestrator can open a TagBlock without further lookups.
type PluginAutoTagMatch struct {
	PluginName  string
	TagID       int64
	Description string
}

// PluginAutoTagResolver is the orchestrator's view of the plugin host:
// "given a focused (process, title) pair, is there a plugin that wants
// to claim it for an auto-tag-block?". Implementations live in the App
// layer because they bridge the plugin host (internal/plugin) and tag
// storage (internal/storage) — neither of which the tagging package
// may import per CLAUDE.md §2.
//
// The resolver is consulted only as a fallback behind user-maintained
// rules: when an enabled rule matches the focused window, the rule
// wins and the resolver is never asked.
//
// Resolve runs synchronously on the orchestrator's focus-change path.
// Implementations should honour the deadline on ctx and return nil
// quickly when no plugin claims the process.
type PluginAutoTagResolver interface {
	Resolve(ctx context.Context, processName, windowTitle string, isCommunication bool) *PluginAutoTagMatch
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

// SetBlockClosedHook installs the callback fired after every block-close
// transition. Pass nil to clear. Safe to call at any time.
func (o *Orchestrator) SetBlockClosedHook(h BlockClosedHook) {
	o.hookMu.Lock()
	o.blockClosedHook = h
	o.hookMu.Unlock()
}

// SetPluginResolver installs the optional plugin-side auto-tag fallback.
// Pass nil to detach. Safe to call at any time — readers take the lock.
func (o *Orchestrator) SetPluginResolver(r PluginAutoTagResolver) {
	o.resolverMu.Lock()
	o.pluginResolver = r
	o.resolverMu.Unlock()
}

// getPluginResolver returns the live resolver (or nil) under
// resolverMu so the orchestrator can ask the App layer without holding
// the main mu — Resolve() may touch the database.
func (o *Orchestrator) getPluginResolver() PluginAutoTagResolver {
	o.resolverMu.RLock()
	r := o.pluginResolver
	o.resolverMu.RUnlock()
	return r
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

	// Notify subscribers (e.g. the oncall Recheck hook). Dispatched in a
	// goroutine so downstream I/O does not extend the o.mu critical
	// section the caller is holding. Hook is read under hookMu, not
	// o.mu, so a hook that wants to call back into the orchestrator
	// will not deadlock.
	o.hookMu.RLock()
	h := o.blockClosedHook
	o.hookMu.RUnlock()
	if h != nil {
		go h(context.Background(), b.ID)
	}
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
	if o.openCommAuto != nil {
		// Communication-driven auto-tag is in force; record focus state so
		// we can re-evaluate when the comm-auto closes, but do not touch
		// the auto-tag-block lifecycle.
		return
	}
	o.advance(ctx, at, o.matchFocusAuto(ctx, name, title))
}

// OnFocusCleared is called by the tracker on idle / lock screen / shutdown.
func (o *Orchestrator) OnFocusCleared(ctx context.Context, at time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.focusActive = false
	o.focusedProcess = focusInfo{}
	if o.openCommAuto != nil {
		// Comm-auto suppresses focus-driven auto changes; do nothing.
		return
	}
	o.advance(ctx, at, nil)
}

// OnCommunicationChanged is called by the tracker whenever the set of active
// communication-process windows changes (open/close/title-change). The
// orchestrator picks the highest-priority rule that matches any session and:
//   - opens a comm-driven auto-tag block (overriding any focus-driven auto;
//     pausing any open manual), or
//   - closes a previously-open comm-auto when no rule matches anymore and
//     re-evaluates focus to resume regular auto-/manual-tag flow.
//
// The same rule still matching means no-op; matching switched rule means
// close-then-open. The tracker passes the full active session list — the
// orchestrator does not maintain its own session set.
func (o *Orchestrator) OnCommunicationChanged(ctx context.Context, sessions []tracker.CommSession, at time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	snap := o.snapFloor(at)
	best := o.matchBestCommAuto(ctx, sessions)

	// Same source still active → no transition.
	if o.openCommAuto != nil && best != nil && best.sourceKey() == o.openCommAuto.sourceKey() {
		return
	}

	// Close any current comm-auto before opening a new one or returning to
	// the focus-driven flow.
	if o.openCommAuto != nil {
		reason := reasonCommWindowGone
		if best != nil {
			reason = reasonCommRuleSwitched
		}
		o.closeCommAuto(ctx, snap, reason)
	}

	if best != nil {
		// Override: close any focus-driven auto, pause any open manual,
		// then open the comm-auto block.
		if o.openAuto != nil {
			o.closeAuto(ctx, snap, reasonAutoOverriddenByComm)
		}
		if o.openManual != nil {
			o.pauseManual(ctx, snap, reasonManualPausedForComm)
		}
		o.startCommAuto(ctx, *best, snap)
		return
	}

	// No comm match active anymore: re-run the focus-driven flow with
	// whatever the tracker last reported.
	var focus *autoMatch
	if o.focusActive {
		focus = o.matchFocusAuto(ctx, o.focusedProcess.name, o.focusedProcess.title)
	}
	o.advance(ctx, at, focus)
}

// advance is the core state-machine step. `match` is the auto-tag
// descriptor that should drive state at `at` — nil when no rule and no
// plugin apply (or focus is cleared). The method holds o.mu.
func (o *Orchestrator) advance(ctx context.Context, at time.Time, match *autoMatch) {
	snap := o.snapFloor(at)

	if o.openAuto != nil {
		reason := reasonAutoFocusLost
		if match != nil && match.sourceKey() == o.openAuto.sourceKey() {
			return
		}
		if match != nil {
			reason = reasonAutoRuleSwitched
		}
		o.closeAuto(ctx, snap, reason)
	}

	if match != nil {
		if o.openManual != nil {
			o.pauseManual(ctx, snap, reasonManualPausedForAuto)
		}
		o.startAuto(ctx, *match, snap)
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

// ResizeBlock changes the start/end of a closed tag block. The new range
// snaps to granularity (start floor, end ceil) and is rejected if it would
// overlap another tag block. Auto-tag blocks are promoted to manual on
// resize — the user's edit is, by definition, a manual intervention.
func (o *Orchestrator) ResizeBlock(ctx context.Context, id int64, from, to time.Time) error {
	if id <= 0 {
		return fmt.Errorf("invalid tag block id: %d", id)
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
	if err := o.blocks.Resize(ctx, id, from, to, true); err != nil {
		return fmt.Errorf("resize tag block %d: %w", id, err)
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
		o.logBlockClosed(*b, snappedEnd, zeroReason, autoSourceLogFields(*state)...)
		o.openAuto = nil
		return
	}
	if err := o.blocks.Close(ctx, state.blockID, snappedEnd); err != nil {
		o.logger.Warn("close auto failed", "id", state.blockID, "err", err)
		o.openAuto = nil
		return
	}
	o.logBlockClosed(*b, snappedEnd, reason, autoSourceLogFields(*state)...)
	o.openAuto = nil
	if o.pausedManual != nil && o.focusActive {
		o.resumeManual(ctx, snappedEnd)
	}
}

// autoSourceLogFields returns the structured log fields identifying the
// origin of an auto-tag block. Rule-driven blocks emit "rule_id";
// plugin-driven ones emit "plugin_name" — exactly one is present so log
// readers can grep either reliably.
func autoSourceLogFields(s autoState) []any {
	if s.pluginName != "" {
		return []any{"plugin_name", s.pluginName}
	}
	return []any{"rule_id", s.ruleID}
}

func (o *Orchestrator) startAuto(ctx context.Context, match autoMatch, snappedStart time.Time) {
	dptr := optionalDescription(match.description)
	block := &storage.TagBlock{
		TagID:       match.tagID,
		Description: dptr,
		StartTime:   snappedStart,
		IsManual:    false,
	}
	if err := o.blocks.Open(ctx, block); err != nil {
		o.logger.Warn("open auto failed",
			"rule_id", match.ruleID, "plugin_name", match.pluginName, "err", err)
		return
	}
	o.openAuto = &autoState{
		blockID:    block.ID,
		ruleID:     match.ruleID,
		pluginName: match.pluginName,
		tagID:      match.tagID,
	}
}

// startCommAuto opens a communication-driven auto-tag block. Mirrors
// startAuto but writes to openCommAuto so the override state machine can
// distinguish the two.
func (o *Orchestrator) startCommAuto(ctx context.Context, match autoMatch, snappedStart time.Time) {
	dptr := optionalDescription(match.description)
	block := &storage.TagBlock{
		TagID:       match.tagID,
		Description: dptr,
		StartTime:   snappedStart,
		IsManual:    false,
	}
	if err := o.blocks.Open(ctx, block); err != nil {
		o.logger.Warn("open comm auto failed",
			"rule_id", match.ruleID, "plugin_name", match.pluginName, "err", err)
		return
	}
	o.openCommAuto = &autoState{
		blockID:    block.ID,
		ruleID:     match.ruleID,
		pluginName: match.pluginName,
		tagID:      match.tagID,
	}
}

// optionalDescription returns a pointer to s, or nil when s is empty
// after whitespace trimming. Centralises the rule/plugin description
// handling so a stray whitespace-only field never reaches the DB.
func optionalDescription(s string) *string {
	d := strings.TrimSpace(s)
	if d == "" {
		return nil
	}
	return &d
}

// closeCommAuto finalizes the open communication-driven auto-tag block.
// Unlike closeAuto it does not attempt to resume a paused manual — the
// caller (OnCommunicationChanged) re-runs the focus flow afterwards, which
// handles manual-resume via the standard advance() path.
func (o *Orchestrator) closeCommAuto(ctx context.Context, snappedEnd time.Time, reason string) {
	if o.openCommAuto == nil {
		return
	}
	state := o.openCommAuto
	b, err := o.blocks.Get(ctx, state.blockID)
	if err != nil || b == nil {
		o.logger.Warn("close comm auto: block missing", "id", state.blockID, "err", err)
		o.openCommAuto = nil
		return
	}
	if !snappedEnd.After(b.StartTime) {
		if err := o.blocks.Delete(ctx, state.blockID); err != nil {
			o.logger.Warn("close comm auto: delete zero-length failed", "id", state.blockID, "err", err)
			o.openCommAuto = nil
			return
		}
		zeroReason := reasonCommWindowGoneZero
		if reason == reasonCommRuleSwitched {
			zeroReason = reasonCommRuleSwitchedZero
		}
		o.logBlockClosed(*b, snappedEnd, zeroReason, autoSourceLogFields(*state)...)
		o.openCommAuto = nil
		return
	}
	if err := o.blocks.Close(ctx, state.blockID, snappedEnd); err != nil {
		o.logger.Warn("close comm auto failed", "id", state.blockID, "err", err)
		o.openCommAuto = nil
		return
	}
	o.logBlockClosed(*b, snappedEnd, reason, autoSourceLogFields(*state)...)
	o.openCommAuto = nil
}

// matchBestCommAuto returns the highest-priority auto-tag match for the
// supplied comm sessions. User rules are evaluated first (priority DESC,
// id ASC via ListEnabled); when no rule matches any session, the plugin
// resolver is consulted per session in deterministic order (sorted by
// process name). Returns nil when neither path produces a match.
func (o *Orchestrator) matchBestCommAuto(ctx context.Context, sessions []tracker.CommSession) *autoMatch {
	if len(sessions) == 0 {
		return nil
	}
	if hit := o.firstCommRuleMatch(ctx, sessions); hit != nil {
		return hit
	}
	return o.firstCommPluginMatch(ctx, sessions)
}

// firstCommRuleMatch evaluates user rules against every comm session and
// returns an autoMatch for the first rule that hits any session. Rules
// are pre-sorted by priority DESC, id ASC by ListEnabled.
func (o *Orchestrator) firstCommRuleMatch(ctx context.Context, sessions []tracker.CommSession) *autoMatch {
	rules, err := o.rules.ListEnabled(ctx)
	if err != nil {
		o.logger.Debug("orchestrator: list rules for comm match failed", "err", err)
		return nil
	}
	if len(rules) == 0 {
		return nil
	}
	compiled, err := Compile(rules)
	if err != nil {
		o.logger.Warn("orchestrator: compile rules for comm match failed", "err", err)
		return nil
	}
	for i := range compiled {
		for _, s := range sessions {
			if compiled[i].Match(s.ProcessName, s.WindowTitle) {
				return ruleToAutoMatch(compiled[i].Rule)
			}
		}
	}
	return nil
}

// firstCommPluginMatch consults the plugin resolver for each comm
// session in deterministic order (sorted by process name then title)
// and returns the first non-nil match. Returns nil if no resolver is
// installed or no session is claimed by any plugin.
func (o *Orchestrator) firstCommPluginMatch(ctx context.Context, sessions []tracker.CommSession) *autoMatch {
	r := o.getPluginResolver()
	if r == nil {
		return nil
	}
	ordered := make([]tracker.CommSession, len(sessions))
	copy(ordered, sessions)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].ProcessName != ordered[j].ProcessName {
			return ordered[i].ProcessName < ordered[j].ProcessName
		}
		return ordered[i].WindowTitle < ordered[j].WindowTitle
	})
	for _, s := range ordered {
		if m := r.Resolve(ctx, s.ProcessName, s.WindowTitle, true); m != nil {
			return &autoMatch{
				pluginName:  m.PluginName,
				tagID:       m.TagID,
				description: m.Description,
			}
		}
	}
	return nil
}

// ruleToAutoMatch packages a rule into the orchestrator's internal
// auto-tag descriptor. Descriptions are trimmed and empty strings are
// dropped to match startAuto's pre-existing behaviour.
func ruleToAutoMatch(r storage.Rule) *autoMatch {
	desc := ""
	if r.Description != nil {
		desc = strings.TrimSpace(*r.Description)
	}
	return &autoMatch{
		ruleID:      r.ID,
		tagID:       r.TagID,
		description: desc,
	}
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
	case reasonManualPausedForComm:
		return reasonManualPausedForCommZero
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

// matchFocusAuto returns the auto-tag descriptor for the given focused
// (process, title) pair. User rules win; the plugin resolver is
// consulted only when no enabled rule matches. Returns nil when neither
// produces a hit.
func (o *Orchestrator) matchFocusAuto(ctx context.Context, name, title string) *autoMatch {
	if hit := o.firstFocusRuleMatch(ctx, name, title); hit != nil {
		return hit
	}
	if r := o.getPluginResolver(); r != nil {
		if m := r.Resolve(ctx, name, title, false); m != nil {
			return &autoMatch{
				pluginName:  m.PluginName,
				tagID:       m.TagID,
				description: m.Description,
			}
		}
	}
	return nil
}

// firstFocusRuleMatch is matchFocusAuto's user-rule arm split out for
// testability.
func (o *Orchestrator) firstFocusRuleMatch(ctx context.Context, name, title string) *autoMatch {
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
		return ruleToAutoMatch(hit.Rule)
	}
	return nil
}

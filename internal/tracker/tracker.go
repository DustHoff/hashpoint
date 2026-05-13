// Package tracker implements the foreground-window polling loop, idle
// detection, process-track lifecycle and crash recovery.
//
// The loop runs in exactly one goroutine started by Run. It owns the current
// open process track; all state mutation happens from this goroutine, so no
// mutex is needed on the in-memory state aside from the pause flag the App
// layer toggles externally.
//
// Process tracking is deliberately raw: the tracker stores poll-clock
// timestamps verbatim, with no granularity snapping and no tagging state.
// Tag-block lifecycle (auto + manual) is owned by the tagging orchestrator
// the tracker notifies on every focus change.
//
// Restrictions per CLAUDE.md §2: tracker must not import internal/app or
// internal/personio. It depends only on storage, tagging and winapi.
package tracker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/dusthoff/hashpoint/internal/storage"
	"github.com/dusthoff/hashpoint/internal/winapi"
)

// FocusSource produces the currently focused window and idle duration.
// Abstracted so tests can supply deterministic stubs.
type FocusSource interface {
	Foreground() (winapi.FocusInfo, error)
	IdleDuration() (time.Duration, error)
}

// CommunicationSource enumerates currently-visible top-level windows whose
// owning process matches one of names. Names are pre-normalized
// (lower-cased, trimmed) by config.NormalizeProcessNames. Abstracted so tests
// can drive the tracker without touching the Win32 API.
type CommunicationSource interface {
	EnumVisibleWindows(names []string) ([]winapi.WindowInfo, error)
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

// realCommSource delegates to package winapi.
type realCommSource struct{}

func (realCommSource) EnumVisibleWindows(names []string) ([]winapi.WindowInfo, error) {
	return winapi.EnumVisibleWindowsForProcesses(names)
}

// CommSession is the orchestrator's view of one active communication window —
// a (process, title) pair against which auto-tag rules can be evaluated. The
// tracker emits the full set on every change so receivers don't need to track
// open/close events themselves.
type CommSession struct {
	ProcessName string
	WindowTitle string
}

// FocusObserver receives notifications whenever the focused window changes,
// focus is lost, or the set of active communication-process windows changes.
// The tagging orchestrator implements this to drive the auto-tag-block
// lifecycle off the same event stream the tracker uses to persist process
// tracks.
type FocusObserver interface {
	// OnFocusChanged is called when a new (process, window) becomes
	// focused. The implicit close of the prior focus has already happened
	// at the same wall-clock instant.
	OnFocusChanged(ctx context.Context, processName, windowTitle string, at time.Time)
	// OnFocusCleared is called when there is no current focus (idle, lock
	// screen, or app shutdown). Any open auto-tag-block should close.
	OnFocusCleared(ctx context.Context, at time.Time)
	// OnCommunicationChanged is called whenever the set of active
	// communication sessions changes (a comm-process window opens, closes,
	// or its title changes). The receiver should re-evaluate its
	// communication-driven auto-tag override based on the full session
	// list — the tracker itself does not maintain rule state.
	OnCommunicationChanged(ctx context.Context, sessions []CommSession, at time.Time)
}

// Config controls runtime behavior of the tracker.
type Config struct {
	PollInterval       time.Duration
	IdleThreshold      time.Duration
	CommunicationNames []string
	// CommunicationTitleExcludes are substrings that, when contained
	// (case-insensitive) in a comm-process window's title, demote the window
	// to a regular process — no comm-track is opened and no comm-driven
	// auto-tag override fires. See spec §2.1a.
	CommunicationTitleExcludes []string
}

// Tracker owns the focus-tracking lifecycle.
type Tracker struct {
	cfg        Config
	source     FocusSource
	commSource CommunicationSource
	clock      Clock
	tracks     storage.ProcessTrackRepository
	observer   FocusObserver
	logger     *slog.Logger

	mu sync.Mutex
	// current holds the tracker's own open focused process track (nil while
	// nothing is being tracked).
	current *storage.ProcessTrack
	// commCurrent maps each visible comm-process window (PID+HWND) to the
	// corresponding open communication track. Title changes close + reopen
	// the row; the lookup key stays stable across that transition.
	commCurrent map[commKey]*commEntry
	// commNames is the lower-cased list of comm process basenames; mutated
	// by SetCommunicationNames for hot-reload from the settings UI.
	commNames []string
	// commTitleExcludesLower is the pre-lowercased list of substrings that
	// disqualify a comm-process window from being treated as a comm-track.
	// The original casing is preserved at the config layer for UI display;
	// here we only need the comparison form. Mutated by
	// SetCommunicationTitleExcludes for hot-reload.
	commTitleExcludesLower []string
	// paused is the user-facing pause state controlled by Pause/Resume.
	// While paused, no process tracking happens (focused or comm); manual
	// tag blocks at the App layer are unaffected.
	paused bool
}

// commKey identifies one comm-process window across ticks. PID alone is not
// enough because a single Teams instance can present multiple visible
// top-level windows (chat, separate meeting window, …); HWND alone is
// theoretically unique but PID-prefixing it makes log correlation easier.
type commKey struct {
	pid  uint32
	hwnd uintptr
}

// commEntry is the live state for one open communication track.
type commEntry struct {
	track *storage.ProcessTrack
	title string
}

// Option is a functional option.
type Option func(*Tracker)

// WithFocusSource overrides the default winapi-backed source (for tests).
func WithFocusSource(s FocusSource) Option { return func(t *Tracker) { t.source = s } }

// WithCommunicationSource overrides the default winapi-backed comm-window
// enumeration (for tests).
func WithCommunicationSource(s CommunicationSource) Option {
	return func(t *Tracker) { t.commSource = s }
}

// WithClock overrides the default clock (for tests).
func WithClock(c Clock) Option { return func(t *Tracker) { t.clock = c } }

// WithObserver wires the tagging orchestrator. Without one the tracker is
// silent on focus events — useful in tests that don't care about tagging.
func WithObserver(o FocusObserver) Option { return func(t *Tracker) { t.observer = o } }

// New constructs a tracker. Run starts the loop.
func New(cfg Config, tracks storage.ProcessTrackRepository, logger *slog.Logger, opts ...Option) *Tracker {
	if logger == nil {
		logger = slog.Default()
	}
	t := &Tracker{
		cfg:                    cfg,
		source:                 realFocusSource{},
		commSource:             realCommSource{},
		clock:                  realClock{},
		tracks:                 tracks,
		logger:                 logger,
		commCurrent:            make(map[commKey]*commEntry),
		commNames:              append([]string(nil), cfg.CommunicationNames...),
		commTitleExcludesLower: lowerCopy(cfg.CommunicationTitleExcludes),
	}
	for _, o := range opts {
		o(t)
	}
	return t
}

// SetCommunicationNames hot-reloads the list of comm process basenames. The
// next tick honours the new set; comm tracks for removed names will close on
// the next reconciliation because their windows no longer match.
func (t *Tracker) SetCommunicationNames(names []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.commNames = append([]string(nil), names...)
}

// SetCommunicationTitleExcludes hot-reloads the list of substrings that
// disqualify a comm-process window from being treated as a comm-track. The
// next tick honours the new set; previously open comm tracks whose title now
// matches an exclude phrase will close on the next reconciliation because
// they're filtered out of the live enumeration.
func (t *Tracker) SetCommunicationTitleExcludes(phrases []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.commTitleExcludesLower = lowerCopy(phrases)
}

// lowerCopy returns a fresh slice with each non-empty entry trimmed and
// lower-cased. Intended for the cached match-form of comm-title-exclude
// phrases — the original casing lives at the config layer.
func lowerCopy(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		out = append(out, strings.ToLower(s))
	}
	return out
}

// Pause stops opening new tracks; the currently open focused track (if any)
// and all open communication tracks are closed at the time Pause is called.
func (t *Tracker) Pause(ctx context.Context) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.paused = true
	now := t.clock.Now()
	if t.current != nil {
		_ = t.tracks.Close(ctx, t.current.ID, now)
		t.current = nil
		t.notifyCleared(ctx, now)
	}
	if len(t.commCurrent) > 0 {
		for key, entry := range t.commCurrent {
			if err := t.tracks.Close(ctx, entry.track.ID, now); err != nil {
				t.logger.Warn("close comm on pause failed", "id", entry.track.ID, "err", err)
			}
			delete(t.commCurrent, key)
		}
		t.notifyCommChanged(ctx, nil, now)
	}
}

// Resume re-enables track opening.
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

// Run starts the polling loop and blocks until ctx is cancelled.
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
	now := t.clock.Now()
	if t.current != nil {
		if err := t.tracks.Close(ctx, t.current.ID, now); err != nil {
			t.logger.Warn("close on shutdown failed", "err", err)
		}
		t.current = nil
		t.notifyCleared(ctx, now)
	}
	if len(t.commCurrent) > 0 {
		for key, entry := range t.commCurrent {
			if err := t.tracks.Close(ctx, entry.track.ID, now); err != nil {
				t.logger.Warn("close comm on shutdown failed", "id", entry.track.ID, "err", err)
			}
			delete(t.commCurrent, key)
		}
		t.notifyCommChanged(ctx, nil, now)
	}
}

// recover finalizes every track left open by a previous crash. Focused tracks
// chain together so each is bounded by the next one's start; comm tracks
// recover independently because they overlap freely with each other and with
// focused tracks.
func (t *Tracker) recover(ctx context.Context) error {
	opens, err := t.tracks.ListOpen(ctx)
	if err != nil {
		return fmt.Errorf("list open tracks: %w", err)
	}
	now := t.clock.Now()
	for i, open := range opens {
		end := open.StartTime.Add(t.cfg.IdleThreshold)
		if end.After(now) {
			end = now
		}
		// If a later open exists, this one ends no later than its start.
		if i+1 < len(opens) && opens[i+1].StartTime.Before(end) {
			end = opens[i+1].StartTime
		}
		t.logger.Info("recovering open process track from previous run",
			"id", open.ID, "process", open.ProcessName,
			"start", open.StartTime.Format(time.RFC3339),
			"recovered_end", end.Format(time.RFC3339),
		)
		if err := t.tracks.Close(ctx, open.ID, end); err != nil {
			return fmt.Errorf("close open track %d: %w", open.ID, err)
		}
	}
	commOpens, err := t.tracks.ListOpenCommunication(ctx)
	if err != nil {
		return fmt.Errorf("list open comm tracks: %w", err)
	}
	for _, open := range commOpens {
		end := open.StartTime.Add(t.cfg.IdleThreshold)
		if end.After(now) {
			end = now
		}
		t.logger.Info("recovering open communication track from previous run",
			"id", open.ID, "process", open.ProcessName,
			"start", open.StartTime.Format(time.RFC3339),
			"recovered_end", end.Format(time.RFC3339),
		)
		if err := t.tracks.Close(ctx, open.ID, end); err != nil {
			return fmt.Errorf("close open comm track %d: %w", open.ID, err)
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
	} else {
		info, err := t.source.Foreground()
		if err != nil && !errors.Is(err, winapi.ErrUnsupported) {
			t.logger.Debug("foreground query failed", "err", err)
		} else if info.IsZero() {
			// E.g. lock screen — close any open focused track.
			t.closeCurrent(ctx)
		} else {
			t.handleFocus(ctx, info)
		}
	}
	// Communication tracking is intentionally independent of idle / focus
	// state: a Teams meeting may continue while the user is idle (just
	// listening) or while the focused window is something else entirely.
	t.tickComm(ctx)
}

// tickComm reconciles the open-comm-track set against the live enumeration of
// visible top-level windows owned by configured comm processes. Opens new
// tracks for newly-visible windows, closes tracks whose window has gone away,
// and treats title changes as close+reopen (mirroring focused-track
// behaviour). Notifies the observer once at the end if anything changed.
func (t *Tracker) tickComm(ctx context.Context) {
	t.mu.Lock()
	names := append([]string(nil), t.commNames...)
	excludes := append([]string(nil), t.commTitleExcludesLower...)
	t.mu.Unlock()

	if len(names) == 0 {
		// Feature off (or all names removed) — close any leftover open
		// comm tracks so the table doesn't collect open-ended rows.
		t.closeAllCommIfAny(ctx)
		return
	}
	windows, err := t.commSource.EnumVisibleWindows(names)
	if err != nil {
		if !errors.Is(err, winapi.ErrUnsupported) {
			t.logger.Debug("enum comm windows failed", "err", err)
		}
		return
	}
	if len(excludes) > 0 {
		// Drop windows whose title contains an exclude phrase. The reconciliation
		// loop below treats any open comm-track for such a window as "no longer
		// alive" (because the key disappears from the seen-set) and closes it on
		// the same tick — that's how a runtime title change demotes a comm-track
		// to a regular focus-only window.
		filtered := windows[:0]
		for _, w := range windows {
			if titleExcluded(w.Title, excludes) {
				continue
			}
			filtered = append(filtered, w)
		}
		windows = filtered
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.clock.Now()
	seen := make(map[commKey]struct{}, len(windows))
	changed := false
	for _, w := range windows {
		key := commKey{pid: w.PID, hwnd: w.HWND}
		seen[key] = struct{}{}
		existing, ok := t.commCurrent[key]
		if !ok {
			track := &storage.ProcessTrack{
				ProcessName:     w.ProcessName,
				ProcessPath:     w.ProcessPath,
				WindowTitle:     w.Title,
				StartTime:       now,
				IsCommunication: true,
			}
			if err := t.tracks.Open(ctx, track); err != nil {
				t.logger.Warn("open comm track failed",
					"process", w.ProcessName, "err", err)
				continue
			}
			t.commCurrent[key] = &commEntry{track: track, title: w.Title}
			changed = true
			t.logger.Debug("opened comm track",
				"id", track.ID, "process", w.ProcessName, "title", w.Title)
			continue
		}
		if existing.title == w.Title {
			continue
		}
		// Title change: close the old row and open a fresh one so the
		// timeline carries the title transition the same way focused
		// tracking does.
		if err := t.tracks.Close(ctx, existing.track.ID, now); err != nil {
			t.logger.Warn("close comm on title change failed",
				"id", existing.track.ID, "err", err)
		}
		track := &storage.ProcessTrack{
			ProcessName:     w.ProcessName,
			ProcessPath:     w.ProcessPath,
			WindowTitle:     w.Title,
			StartTime:       now,
			IsCommunication: true,
		}
		if err := t.tracks.Open(ctx, track); err != nil {
			t.logger.Warn("reopen comm track on title change failed",
				"process", w.ProcessName, "err", err)
			delete(t.commCurrent, key)
			changed = true
			continue
		}
		t.commCurrent[key] = &commEntry{track: track, title: w.Title}
		changed = true
	}
	for key, entry := range t.commCurrent {
		if _, alive := seen[key]; alive {
			continue
		}
		if err := t.tracks.Close(ctx, entry.track.ID, now); err != nil {
			t.logger.Warn("close gone comm track failed",
				"id", entry.track.ID, "err", err)
		}
		delete(t.commCurrent, key)
		changed = true
		t.logger.Debug("closed comm track", "id", entry.track.ID)
	}
	if changed {
		t.notifyCommChanged(ctx, t.snapshotCommSessionsLocked(), now)
	}
}

// titleExcluded reports whether title contains any of the (already lower-cased)
// exclude phrases as a case-insensitive substring. Empty phrases are filtered
// out by the caller (lowerCopy / NormalizeTitleExcludePhrases).
func titleExcluded(title string, lowerExcludes []string) bool {
	if len(lowerExcludes) == 0 {
		return false
	}
	lt := strings.ToLower(title)
	for _, p := range lowerExcludes {
		if strings.Contains(lt, p) {
			return true
		}
	}
	return false
}

// closeAllCommIfAny closes every open comm track. Used when the feature is
// turned off (empty name list) and on shutdown/pause.
func (t *Tracker) closeAllCommIfAny(ctx context.Context) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.commCurrent) == 0 {
		return
	}
	now := t.clock.Now()
	for key, entry := range t.commCurrent {
		if err := t.tracks.Close(ctx, entry.track.ID, now); err != nil {
			t.logger.Warn("close comm (feature off) failed",
				"id", entry.track.ID, "err", err)
		}
		delete(t.commCurrent, key)
	}
	t.notifyCommChanged(ctx, nil, now)
}

// snapshotCommSessionsLocked returns the current comm sessions — caller must
// hold t.mu. Order follows map iteration; receivers are expected to sort by
// rule priority via their own logic.
func (t *Tracker) snapshotCommSessionsLocked() []CommSession {
	if len(t.commCurrent) == 0 {
		return nil
	}
	out := make([]CommSession, 0, len(t.commCurrent))
	for _, entry := range t.commCurrent {
		out = append(out, CommSession{
			ProcessName: entry.track.ProcessName,
			WindowTitle: entry.title,
		})
	}
	return out
}

func (t *Tracker) handleIdle(ctx context.Context) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.current == nil {
		return
	}
	now := t.clock.Now()
	if err := t.tracks.MarkIdle(ctx, t.current.ID, now); err != nil {
		t.logger.Warn("mark idle failed", "err", err)
		return
	}
	t.logger.Debug("track marked idle", "id", t.current.ID)
	t.current = nil
	t.notifyCleared(ctx, now)
}

func (t *Tracker) closeCurrent(ctx context.Context) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.current == nil {
		return
	}
	now := t.clock.Now()
	if err := t.tracks.Close(ctx, t.current.ID, now); err != nil {
		t.logger.Warn("close failed", "err", err)
	}
	t.current = nil
	t.notifyCleared(ctx, now)
}

func (t *Tracker) handleFocus(ctx context.Context, info winapi.FocusInfo) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Same focus → keep current track open.
	if t.current != nil &&
		t.current.ProcessName == info.ProcessName &&
		t.current.WindowTitle == info.Title {
		return
	}

	now := t.clock.Now()
	if t.current != nil {
		if err := t.tracks.Close(ctx, t.current.ID, now); err != nil {
			t.logger.Warn("close on switch failed", "err", err)
		}
	}

	p := &storage.ProcessTrack{
		ProcessName: info.ProcessName,
		ProcessPath: info.ProcessPath,
		WindowTitle: info.Title,
		StartTime:   now,
	}
	if err := t.tracks.Open(ctx, p); err != nil {
		t.logger.Warn("open process track failed", "err", err)
		t.current = nil
		t.notifyCleared(ctx, now)
		return
	}
	t.current = p

	// Title is debug-only by spec §5: never log on info+.
	t.logger.Debug("opened process track",
		"id", p.ID, "process", p.ProcessName, "title", p.WindowTitle)

	t.notifyChanged(ctx, info.ProcessName, info.Title, now)
}

func (t *Tracker) notifyChanged(ctx context.Context, name, title string, at time.Time) {
	if t.observer == nil {
		return
	}
	t.observer.OnFocusChanged(ctx, name, title, at)
}

func (t *Tracker) notifyCleared(ctx context.Context, at time.Time) {
	if t.observer == nil {
		return
	}
	t.observer.OnFocusCleared(ctx, at)
}

func (t *Tracker) notifyCommChanged(ctx context.Context, sessions []CommSession, at time.Time) {
	if t.observer == nil {
		return
	}
	t.observer.OnCommunicationChanged(ctx, sessions, at)
}

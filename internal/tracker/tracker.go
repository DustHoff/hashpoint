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
	"sync"
	"time"

	"github.com/onesi/hashpoint/internal/storage"
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

// FocusObserver receives notifications whenever the focused window changes
// or focus is lost. The tagging orchestrator implements this to drive the
// auto-tag-block lifecycle off the same event stream the tracker uses to
// persist process tracks.
type FocusObserver interface {
	// OnFocusChanged is called when a new (process, window) becomes
	// focused. The implicit close of the prior focus has already happened
	// at the same wall-clock instant.
	OnFocusChanged(ctx context.Context, processName, windowTitle string, at time.Time)
	// OnFocusCleared is called when there is no current focus (idle, lock
	// screen, or app shutdown). Any open auto-tag-block should close.
	OnFocusCleared(ctx context.Context, at time.Time)
}

// Config controls runtime behavior of the tracker.
type Config struct {
	PollInterval  time.Duration
	IdleThreshold time.Duration
}

// Tracker owns the focus-tracking lifecycle.
type Tracker struct {
	cfg      Config
	source   FocusSource
	clock    Clock
	tracks   storage.ProcessTrackRepository
	observer FocusObserver
	logger   *slog.Logger

	mu sync.Mutex
	// current holds the tracker's own open process track (nil while nothing
	// is being tracked).
	current *storage.ProcessTrack
	// paused is the user-facing pause state controlled by Pause/Resume.
	// While paused, no process tracking happens; manual tag blocks at the
	// App layer are unaffected.
	paused bool
}

// Option is a functional option.
type Option func(*Tracker)

// WithFocusSource overrides the default winapi-backed source (for tests).
func WithFocusSource(s FocusSource) Option { return func(t *Tracker) { t.source = s } }

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
		cfg:    cfg,
		source: realFocusSource{},
		clock:  realClock{},
		tracks: tracks,
		logger: logger,
	}
	for _, o := range opts {
		o(t)
	}
	return t
}

// Pause stops opening new tracks; the currently open track (if any) is
// closed at the time Pause is called.
func (t *Tracker) Pause(ctx context.Context) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.paused = true
	if t.current != nil {
		now := t.clock.Now()
		_ = t.tracks.Close(ctx, t.current.ID, now)
		t.current = nil
		t.notifyCleared(ctx, now)
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
	if t.current != nil {
		now := t.clock.Now()
		if err := t.tracks.Close(ctx, t.current.ID, now); err != nil {
			t.logger.Warn("close on shutdown failed", "err", err)
		}
		t.current = nil
		t.notifyCleared(ctx, now)
	}
}

// recover finalizes every track left open by a previous crash. Closing only
// the latest open leaves earlier opens dangling; we close every one.
func (t *Tracker) recover(ctx context.Context) error {
	opens, err := t.tracks.ListOpen(ctx)
	if err != nil {
		return fmt.Errorf("list open tracks: %w", err)
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
		// E.g. lock screen — close any open track.
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

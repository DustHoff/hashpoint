// Package watchdog implements the out-of-process supervisor for the collector
// (issue #21, ADR 0001 "Stufe C"). The collector is the long-lived,
// data-owning half of the split and can be terminated by the OS during Modern
// Standby without running any cleanup. Hosted as a Windows service (so it
// outlives that kill), the watchdog polls the collector's liveness and, on an
// unclean death, closes any still-open tag-blocks at the collector's last
// confirmed-alive time and relaunches it into the user's session.
//
// This package holds the platform-agnostic monitor loop and the tag-block
// closer; the Windows-specific session/process operations are injected through
// the Probe interface (implemented in cmd/timetracker on Windows), keeping this
// package unit-testable on any GOOS.
package watchdog

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dusthoff/hashpoint/internal/storage"
)

// ErrNoActiveSession signals that no interactive user is logged on, so there is
// no session to relaunch the collector into. The monitor treats it as idle.
// The Windows Probe maps winapi.ErrNoActiveSession onto this so the monitor
// stays free of any Windows dependency.
var ErrNoActiveSession = errors.New("watchdog: no active console session")

// Target locates the per-user artifacts the watchdog operates on. It is
// resolved fresh each tick because the active user (and thus the data dir) can
// change at runtime via fast-user-switching.
type Target struct {
	DataDir    string // %LOCALAPPDATA%\TimeTracker of the interactive user
	DBFile     string // SQLite database holding the tag-blocks
	MarkerPath string // collector crashguard sentinel (.alive) path
	Exe        string // collector executable to relaunch
}

// Probe abstracts the platform-specific session, marker and process operations
// so the monitor loop can be exercised with fakes. All methods take a context
// for cancellation on service stop.
type Probe interface {
	// ResolveTarget locates the interactive user's data dir, DB, marker and
	// executable. It returns ErrNoActiveSession when no user is logged on.
	ResolveTarget(ctx context.Context) (Target, error)
	// CollectorAlive reports whether the collector process is alive. The
	// returned lastAlive is the marker's heartbeat timestamp, used as a
	// fallback bound when the collector is found dead. It returns
	// crashguard.ErrMarkerAbsent (clean quit / never started) or
	// crashguard.ErrMarkerCorrupt (torn read) so the monitor can skip the tick.
	CollectorAlive(ctx context.Context, t Target) (alive bool, lastAlive time.Time, err error)
	// Relaunch starts the collector in the interactive user session.
	Relaunch(ctx context.Context, t Target) error
}

// TagBlockStore is the subset of storage.TagBlockRepository the closer needs.
type TagBlockStore interface {
	ListOpen(ctx context.Context) ([]storage.TagBlock, error)
	SetEnd(ctx context.Context, id int64, end time.Time) error
}

// CloseOpenBlocks sets the end time of every open tag-block in store to at
// (coerced to UTC), returning how many were closed. It mirrors the orchestrator
// crash-recovery idiom (ListOpen → SetEnd) and is idempotent: a block already
// closed is no longer returned by ListOpen, so the collector's own recovery on
// the next start is a harmless no-op backstop.
func CloseOpenBlocks(ctx context.Context, store TagBlockStore, at time.Time) (int, error) {
	open, err := store.ListOpen(ctx)
	if err != nil {
		return 0, fmt.Errorf("list open tag-blocks: %w", err)
	}
	closed := 0
	for i := range open {
		if err := store.SetEnd(ctx, open[i].ID, at.UTC()); err != nil {
			return closed, fmt.Errorf("close tag-block %d: %w", open[i].ID, err)
		}
		closed++
	}
	return closed, nil
}

// pickLastAlive returns the tightest lower bound on when the collector was last
// alive: the later of the watchdog's own last observation and the marker
// heartbeat, clamped to now (and never in the future). A zero result falls back
// to now. The watchdog's own observation is finer-grained than the 30s marker
// heartbeat, so it is preferred when available.
func pickLastAlive(observed, marker, now time.Time) time.Time {
	best := observed
	if marker.After(best) {
		best = marker
	}
	if best.IsZero() || best.After(now) {
		return now.UTC()
	}
	return best.UTC()
}

package crashguard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Role identifies which half of the application a marker belongs to, so the
// monolith, collector and UI processes never collide on the same sentinel file.
type Role string

// Process roles.
const (
	RoleMonolith  Role = "monolith"
	RoleCollector Role = "collector"
	RoleUI        Role = "ui"
)

// markerDirName is the subdirectory of the data dir holding the per-role
// sentinel files.
const markerDirName = "runtime"

// heartbeatInterval is how often an armed marker refreshes last_alive. Coarse on
// purpose: the write is tiny but pointless to do more often, and the value only
// needs to approximate "alive around here" for the downtime estimate in an
// unclean-shutdown report.
const heartbeatInterval = 30 * time.Second

// Info is the immutable identity of the current run, embedded in the marker so
// an unclean-shutdown report can name the build that died.
type Info struct {
	Version string
	Commit  string
}

// markerFile is the on-disk JSON shape of a sentinel.
type markerFile struct {
	PID       int       `json:"pid"`
	Mode      string    `json:"mode"`
	Version   string    `json:"version"`
	Commit    string    `json:"commit"`
	StartUTC  time.Time `json:"start_utc"`
	LastAlive time.Time `json:"last_alive_utc"`
}

// Marker is an armed per-role sentinel. The heartbeat goroutine advances its
// last_alive; Disarm removes it on a clean shutdown. Build one via Start.
type Marker struct {
	path   string
	logger *slog.Logger

	mu       sync.Mutex
	data     markerFile
	disarmed bool
}

// Start runs the full startup sequence for a process role: it reports a
// previous run that did not exit cleanly (if any), arms a fresh marker, and
// launches a heartbeat goroutine bound to ctx. The returned Marker's Disarm
// must be called on every clean shutdown path (a deferred call is fine — Disarm
// is nil-safe and idempotent). An arm failure is returned but is not fatal to
// the caller; crash detection is simply unavailable for this run.
func Start(ctx context.Context, dataDir string, role Role, info Info, logger *slog.Logger) (*Marker, error) {
	if logger == nil {
		logger = slog.Default()
	}
	path := MarkerPath(dataDir, role)

	reportPrevious(path, role, logger)

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create runtime dir: %w", err)
	}
	now := time.Now().UTC()
	m := &Marker{
		path:   path,
		logger: logger,
		data: markerFile{
			PID:       os.Getpid(),
			Mode:      string(role),
			Version:   info.Version,
			Commit:    info.Commit,
			StartUTC:  now,
			LastAlive: now,
		},
	}
	if err := m.write(); err != nil {
		return nil, fmt.Errorf("arm marker: %w", err)
	}
	go m.heartbeatLoop(ctx)
	return m, nil
}

// MarkerPath returns the on-disk path of the sentinel file for role under
// dataDir. It is exported so an out-of-process supervisor (the watchdog) can
// locate a collector's marker without duplicating the layout.
func MarkerPath(dataDir string, role Role) string {
	return filepath.Join(dataDir, markerDirName, string(role)+".alive")
}

// MarkerSnapshot is a point-in-time read of a sentinel file.
type MarkerSnapshot struct {
	PID       int       // process ID that armed the marker
	Mode      string    // role string ("collector", "monolith", "ui")
	Version   string    // build version of the armed process
	Commit    string    // build commit of the armed process
	StartUTC  time.Time // when the marker was armed
	LastAlive time.Time // last heartbeat timestamp
}

// ErrMarkerAbsent is returned by ReadMarker when no sentinel file exists — the
// normal state after a clean shutdown (Disarm removes the file) or before the
// role has ever started.
var ErrMarkerAbsent = errors.New("crashguard: marker absent")

// ErrMarkerCorrupt is returned by ReadMarker when the sentinel file exists but
// cannot be parsed. The marker is written non-atomically (see writeLocked), so
// a reader can observe a torn write; callers should retry rather than conclude
// the process is dead.
var ErrMarkerCorrupt = errors.New("crashguard: marker corrupt")

// ReadMarker reads and parses the sentinel file at path. It returns
// ErrMarkerAbsent when the file does not exist and ErrMarkerCorrupt when it
// exists but is not valid JSON. The on-disk format is the single source of
// truth shared with the watchdog, which probes a collector's liveness across
// the Session-0 boundary where the single-instance mutex is invisible.
func ReadMarker(path string) (MarkerSnapshot, error) {
	raw, err := os.ReadFile(path) // #nosec G304 -- path is built from the app data dir and a fixed role name; no user input.
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return MarkerSnapshot{}, ErrMarkerAbsent
		}
		return MarkerSnapshot{}, fmt.Errorf("read marker: %w", err)
	}
	var f markerFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return MarkerSnapshot{}, ErrMarkerCorrupt
	}
	return MarkerSnapshot(f), nil
}

// reportPrevious reads a leftover marker and, if present, logs an
// unclean-shutdown record. A missing file is the normal clean case. A corrupt
// file is still reported (best-effort): its mere presence proves the previous
// run did not disarm.
func reportPrevious(path string, role Role, logger *slog.Logger) {
	prev, err := ReadMarker(path)
	if err != nil {
		switch {
		case errors.Is(err, ErrMarkerAbsent):
			return // normal clean case
		case errors.Is(err, ErrMarkerCorrupt):
			logger.Error("previous run did not exit cleanly",
				"event", EventUncleanShutdown, "role", role, "marker", "corrupt")
		default:
			logger.Warn("crashguard: could not read previous marker", "role", role, "err", err)
		}
		return
	}
	now := time.Now().UTC()
	attrs := []any{
		"event", EventUncleanShutdown,
		"role", role,
		"prev_pid", prev.PID,
		"prev_version", prev.Version,
		"prev_commit", prev.Commit,
	}
	if !prev.StartUTC.IsZero() {
		attrs = append(attrs, "prev_start", prev.StartUTC.Format(time.RFC3339))
	}
	if !prev.LastAlive.IsZero() {
		attrs = append(attrs,
			"last_alive", prev.LastAlive.Format(time.RFC3339),
			"downtime_sec", int(now.Sub(prev.LastAlive).Seconds()))
	}
	logger.Error("previous run did not exit cleanly", attrs...)
}

func (m *Marker) write() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.writeLocked()
}

// writeLocked persists the current marker state. Caller must hold m.mu. A torn
// write (process killed mid-write) leaves a present-but-corrupt file, which the
// next start still correctly reports as an unclean shutdown.
func (m *Marker) writeLocked() error {
	b, err := json.Marshal(m.data)
	if err != nil {
		return err
	}
	return os.WriteFile(m.path, b, 0o600)
}

// Heartbeat advances last_alive to now. Called by the heartbeat loop; a no-op
// once the marker has been disarmed so a late tick can never resurrect a
// removed sentinel.
func (m *Marker) Heartbeat() {
	m.mu.Lock()
	if m.disarmed {
		m.mu.Unlock()
		return
	}
	m.data.LastAlive = time.Now().UTC()
	err := m.writeLocked()
	m.mu.Unlock()
	if err != nil {
		m.logger.Debug("crashguard: heartbeat write failed", "err", err)
	}
}

func (m *Marker) heartbeatLoop(ctx context.Context) {
	defer Recover(m.logger, "crashguard-heartbeat")
	t := time.NewTicker(heartbeatInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.Heartbeat()
		}
	}
}

// Disarm removes the marker, signalling a clean shutdown. Nil-safe and
// idempotent: a nil receiver, a second call, or a call when the file is already
// gone are all no-ops. Disarming also blocks any further heartbeat write, so it
// is safe to call concurrently with the heartbeat loop.
func (m *Marker) Disarm() {
	if m == nil {
		return
	}
	m.mu.Lock()
	already := m.disarmed
	m.disarmed = true
	m.mu.Unlock()
	if already {
		return
	}
	if err := os.Remove(m.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		m.logger.Warn("crashguard: could not remove marker on shutdown", "err", err)
	}
}

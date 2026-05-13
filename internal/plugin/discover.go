package plugin

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"time"
)

// defaultDiscoveryInterval is the period the host re-scans PluginsDir for
// freshly-dropped plugin directories. Anything below ~5s wastes CPU
// without giving the user a noticeably faster experience; anything
// above a minute makes manual "just unzip into the folder" workflows
// feel sluggish.
const defaultDiscoveryInterval = 30 * time.Second

// startDiscoveryLoop spawns the periodic plugin-discovery goroutine.
// Called once from Start() when DiscoveryInterval >= 0.
//
// Cancellation semantics: the goroutine returns as soon as ctx is
// cancelled. Host.Stop() cancels the context held on the Host before
// killing subprocesses, so the loop never tries to launch a plugin
// during shutdown.
func (h *Host) startDiscoveryLoop(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				h.discoverNew(ctx)
			}
		}
	}()
}

// discoverNew rescans PluginsDir and launches any subdirectory that is
// not already known to the host. Directories that fail to launch land
// in StateFailed via the regular launch path — the next tick does NOT
// retry them automatically (the failed entry blocks rediscovery), which
// matches the deliberate decision to leave manual-deletion cleanup out
// of scope.
//
// "Auto-enable" is a no-op write: the storage layer returns enabled=true
// for any plugin without a plugin_state row, so freshly-discovered
// plugins boot straight into StateRunning (or needs_config) without
// touching the database.
func (h *Host) discoverNew(ctx context.Context) {
	entries, err := os.ReadDir(h.deps.PluginsDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// PluginsDir may legitimately not exist yet (clean install).
			// Silently skip — the next tick will pick it up if the user
			// creates the directory.
			return
		}
		h.log.Warn("discovery: read plugins dir failed", "err", err)
		return
	}

	var fresh []string
	h.mu.RLock()
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, known := h.plugins[e.Name()]; known {
			continue
		}
		fresh = append(fresh, e.Name())
	}
	h.mu.RUnlock()

	for _, name := range fresh {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if err := h.launch(ctx, name); err != nil {
			h.log.Warn("discovery: plugin launch failed",
				"name", name, "err", err)
			// The launch path already recorded the failure; continue
			// with the next candidate.
		}
		if h.deps.OnDiscovered != nil {
			if info, ok := h.Get(name); ok {
				h.deps.OnDiscovered(info)
			}
		}
	}
}

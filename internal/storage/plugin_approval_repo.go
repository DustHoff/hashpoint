package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// pluginApprovedKey is the settings key under which the JSON array of
// user-approved plugin names is persisted.
const pluginApprovedKey = "plugins.approved"

// settingsKV is the minimal key/value surface PluginApprovalRepo needs.
// Satisfied by *SettingsRepo.
type settingsKV interface {
	Get(ctx context.Context, key string) (string, bool, error)
	Set(ctx context.Context, key, value string) error
}

// PluginApprovalRepo persists the set of plugin names the user has
// explicitly approved to run, as a JSON array under a single settings key.
// The plugin host refuses to launch a plugin that is not approved, so a
// plugin directory that merely appears under PluginsDir cannot auto-execute.
type PluginApprovalRepo struct {
	settings settingsKV
	mu       sync.Mutex
}

// NewPluginApprovalRepo wires a PluginApprovalRepo over a settings store.
func NewPluginApprovalRepo(settings settingsKV) *PluginApprovalRepo {
	return &PluginApprovalRepo{settings: settings}
}

// IsApproved reports whether name is in the approved set.
func (r *PluginApprovalRepo) IsApproved(ctx context.Context, name string) (bool, error) {
	approved, err := r.ListApproved(ctx)
	if err != nil {
		return false, err
	}
	for _, n := range approved {
		if n == name {
			return true, nil
		}
	}
	return false, nil
}

// Approve adds name to the approved set. It is idempotent.
func (r *PluginApprovalRepo) Approve(ctx context.Context, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	approved, err := r.loadLocked(ctx)
	if err != nil {
		return err
	}
	for _, n := range approved {
		if n == name {
			return nil
		}
	}
	approved = append(approved, name)
	blob, err := json.Marshal(approved)
	if err != nil {
		return fmt.Errorf("marshal approved plugins: %w", err)
	}
	if err := r.settings.Set(ctx, pluginApprovedKey, string(blob)); err != nil {
		return fmt.Errorf("persist approved plugins: %w", err)
	}
	return nil
}

// ListApproved returns the approved plugin names (empty when none).
func (r *PluginApprovalRepo) ListApproved(ctx context.Context) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.loadLocked(ctx)
}

// loadLocked reads and decodes the approved-set blob. The caller must hold
// r.mu (or accept the read-only race ListApproved tolerates via its own
// lock).
func (r *PluginApprovalRepo) loadLocked(ctx context.Context) ([]string, error) {
	raw, ok, err := r.settings.Get(ctx, pluginApprovedKey)
	if err != nil {
		return nil, fmt.Errorf("read approved plugins: %w", err)
	}
	if !ok || raw == "" {
		return nil, nil
	}
	var names []string
	if err := json.Unmarshal([]byte(raw), &names); err != nil {
		return nil, fmt.Errorf("decode approved plugins: %w", err)
	}
	return names, nil
}

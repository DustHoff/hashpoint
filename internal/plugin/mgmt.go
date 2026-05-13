package plugin

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/dusthoff/hashpoint/plugin/sdk"
	hplugin "github.com/hashicorp/go-plugin"
)

// AvailablePluginEntry is the read-model rendered in the "Verfügbare
// Plugins" tab. It is sdk.AvailablePlugin enriched with the source
// plugin's name (so Install/Update/Uninstall can be routed back) and
// the locally installed version (empty when the plugin is not installed,
// equal to AvailablePlugin.Version when up-to-date).
type AvailablePluginEntry struct {
	sdk.AvailablePlugin
	// SourcePlugin is the name of the running plugin that surfaced this
	// entry via its PluginManagementHandler. Install/Update/Uninstall
	// route back to this plugin.
	SourcePlugin string `json:"source_plugin"`
	// InstalledVersion is the version of the plugin already loaded on
	// disk under the same Name, or "" if no plugin with that name is
	// known to the host. The UI uses this to decide between Install
	// and Update buttons.
	InstalledVersion string `json:"installed_version"`
}

// ErrUnknownPluginSource is returned when the caller names a source
// plugin that is not running or does not advertise CapPluginManagement.
// Typically the catalog and the action click came from different
// instants in time and the source plugin was disabled in between.
var ErrUnknownPluginSource = errors.New("plugin: source plugin not running with plugin_management capability")

// ErrSelfUninstallRefused is returned by UninstallPlugin when the target
// plugin equals the source plugin — uninstalling the handler with itself
// would leave the catalog in a half-broken state mid-flight.
var ErrSelfUninstallRefused = errors.New("plugin: a plugin source cannot uninstall itself")

// ListAvailablePlugins fans out to every running plugin advertising
// CapPluginManagement, merges their catalogs, and stamps each entry
// with its source plugin and the locally installed version. Entries
// from multiple sources with the same Name are kept as separate rows
// — the UI surfaces them with their distinct SourcePlugin badges so
// the user can pick which source they trust.
//
// Returns nil (not an error) when no source plugin is running; the UI
// renders an empty state in that case.
func (h *Host) ListAvailablePlugins(ctx context.Context) ([]AvailablePluginEntry, error) {
	type source struct {
		name    string
		handler sdk.PluginManagementHandler
	}
	var sources []source
	installed := map[string]string{}

	h.mu.RLock()
	for name, p := range h.plugins {
		if p.state == StateRunning && p.mgmt != nil {
			sources = append(sources, source{name: name, handler: p.mgmt})
		}
		if p.manifest != nil {
			installed[name] = p.manifest.Version
		}
		if p.state == StateRunning && p.meta.Version != "" {
			installed[name] = p.meta.Version
		}
	}
	h.mu.RUnlock()

	if len(sources) == 0 {
		return nil, nil
	}

	var out []AvailablePluginEntry
	for _, s := range sources {
		entries, err := s.handler.ListAvailable(ctx)
		if err != nil {
			// One broken source must not deny the user the catalog from
			// the other working ones. Log and continue.
			h.log.Warn("plugin source ListAvailable failed",
				"source", s.name, "err", err)
			continue
		}
		for _, e := range entries {
			out = append(out, AvailablePluginEntry{
				AvailablePlugin:  e,
				SourcePlugin:     s.name,
				InstalledVersion: installed[e.Name],
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].SourcePlugin < out[j].SourcePlugin
	})
	return out, nil
}

// InstallPlugin asks the named source plugin to materialise <name> under
// PluginsDir and then launches the freshly-written plugin. If <name> is
// already loaded, the call is rejected — installing on top of a known
// plugin would silently overwrite its files; the user should call
// UpdatePlugin instead.
func (h *Host) InstallPlugin(ctx context.Context, sourcePlugin, name string) error {
	if name == "" {
		return errors.New("plugin: empty name")
	}

	h.mu.RLock()
	_, exists := h.plugins[name]
	h.mu.RUnlock()
	if exists {
		return fmt.Errorf("plugin %q already installed — use Update instead", name)
	}

	handler, err := h.mgmtHandler(sourcePlugin)
	if err != nil {
		return err
	}
	if err := handler.Install(ctx, name); err != nil {
		return fmt.Errorf("install via %s: %w", sourcePlugin, err)
	}
	if err := h.launch(ctx, name); err != nil {
		return fmt.Errorf("launch after install: %w", err)
	}
	return nil
}

// UpdatePlugin stops the target subprocess (so the source plugin can
// overwrite the Windows-locked .exe), asks the source plugin to refresh
// the files, and then relaunches the target. The target plugin must
// already be known to the host — if it is not installed, the caller
// should use InstallPlugin instead.
func (h *Host) UpdatePlugin(ctx context.Context, sourcePlugin, name string) error {
	if name == "" {
		return errors.New("plugin: empty name")
	}

	handler, err := h.mgmtHandler(sourcePlugin)
	if err != nil {
		return err
	}
	h.mu.RLock()
	_, known := h.plugins[name]
	h.mu.RUnlock()
	if !known {
		return fmt.Errorf("plugin %q not installed — use Install instead", name)
	}

	h.stopAndForget(name)
	if err := handler.Update(ctx, name); err != nil {
		// On Update failure the binary may be missing or stale; relaunch
		// best-effort so the user can still see the plugin's state and
		// diagnose. The relaunch error is logged but not propagated —
		// the original handler error is the actionable one.
		if relErr := h.launch(ctx, name); relErr != nil {
			h.log.Warn("relaunch after failed update also failed",
				"plugin", name, "err", relErr)
		}
		return fmt.Errorf("update via %s: %w", sourcePlugin, err)
	}
	if err := h.launch(ctx, name); err != nil {
		return fmt.Errorf("launch after update: %w", err)
	}
	return nil
}

// UninstallPlugin refuses self-uninstall, stops the target subprocess
// if running, asks the source plugin to remove the files, and then
// clears the plugin's persisted state (plugin_state + plugin_settings).
// After this returns the plugin is fully gone from the host's view; a
// future Install starts from manifest defaults.
func (h *Host) UninstallPlugin(ctx context.Context, sourcePlugin, name string) error {
	if name == "" {
		return errors.New("plugin: empty name")
	}
	if name == sourcePlugin {
		return ErrSelfUninstallRefused
	}

	handler, err := h.mgmtHandler(sourcePlugin)
	if err != nil {
		return err
	}

	h.stopAndForget(name)
	if err := handler.Uninstall(ctx, name); err != nil {
		return fmt.Errorf("uninstall via %s: %w", sourcePlugin, err)
	}
	if err := h.deps.Settings.Clear(ctx, name); err != nil {
		// Files are already gone; the DB residue will look strange in
		// the UI but won't actively break anything. Surface the error
		// so the user can re-try a manual cleanup.
		return fmt.Errorf("clear settings after uninstall: %w", err)
	}
	return nil
}

// mgmtHandler returns the PluginManagementHandler for sourcePlugin, or
// ErrUnknownPluginSource if it is not running with that capability.
func (h *Host) mgmtHandler(sourcePlugin string) (sdk.PluginManagementHandler, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	p, ok := h.plugins[sourcePlugin]
	if !ok || p.state != StateRunning || p.mgmt == nil {
		return nil, fmt.Errorf("%w: %q", ErrUnknownPluginSource, sourcePlugin)
	}
	return p.mgmt, nil
}

// stopAndForget releases everything the host holds for the named plugin:
// kills the subprocess if alive, revokes any outstanding SecretHandles,
// and removes the in-memory entry. The persisted plugin_state and
// plugin_settings rows are left untouched — callers that want a full
// wipe must call Settings.Clear separately.
//
// Shared by UpdatePlugin (so handler.Update can overwrite the locked
// .exe) and UninstallPlugin (so handler.Uninstall can rm -rf the dir).
func (h *Host) stopAndForget(name string) {
	h.mu.Lock()
	p, ok := h.plugins[name]
	if !ok {
		h.mu.Unlock()
		return
	}
	var client *hplugin.Client
	if p.client != nil {
		client = p.client
	}
	delete(h.plugins, name)
	h.mu.Unlock()
	if client != nil {
		client.Kill()
	}
	h.handles.revokeFor(name)
}

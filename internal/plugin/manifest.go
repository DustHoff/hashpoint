// Package plugin owns the host-side wiring for Hashpoint's plugin system:
// discovery of installed plugin binaries, subprocess launching via
// hashicorp/go-plugin, lifecycle management, and routing capability calls
// (today: oncall_documentation Submit) to every plugin that advertises
// the relevant capability.
//
// The plugin author-facing types (Plugin interface, Metadata, HostAPI, …)
// live in plugin/sdk so they can also be imported by plugin
// binaries without dragging in host-only dependencies.
package plugin

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/dusthoff/hashpoint/plugin/sdk"
)

// Manifest is the contents of <plugin-dir>/manifest.toml. The file
// describes the plugin to the host without requiring the host to launch
// it — the settings UI can render the config form from this alone.
type Manifest struct {
	// Name MUST match the plugin's directory name. The host treats the
	// directory as the source of truth and rejects a manifest whose Name
	// disagrees (catches "I renamed the folder but not the manifest").
	Name string `toml:"name"`
	// Version is informational, semver by convention.
	Version string `toml:"version"`
	// APIVersion MUST equal sdk.HostAPIVersion or the host refuses to load.
	APIVersion int `toml:"api_version"`
	// Description is the one-line text shown in the settings UI.
	Description string `toml:"description"`
	// Capabilities are the sdk.Capability strings the plugin advertises.
	Capabilities []string `toml:"capabilities"`
	// ConfigSchema describes the per-plugin settings the user fills in
	// via the Plugins tab. Values are persisted in the plugin_settings
	// table; password-typed fields are encrypted at rest.
	ConfigSchema ManifestConfigSchema `toml:"config_schema"`
}

// ManifestConfigSchema is the form shape the settings UI renders. There
// is intentionally no separate "secrets" map: every field — secret or
// not — lives here, distinguished by its Type. The host derives
// is_secret from sdk.FieldTypePassword when persisting.
type ManifestConfigSchema struct {
	Fields map[string]ManifestField `toml:"fields" json:"fields"`
}

// ManifestField describes one config field. Type drives both the UI
// input element AND the persistence + delivery strategy:
//
//   - text / boolean → PluginConfig.Fields (plain in DB)
//   - password       → PluginConfig.Secrets (encrypted in DB, surfaced
//     to the plugin as a SecretHandle the plugin redeems on demand)
type ManifestField struct {
	Label    string        `toml:"label"    json:"label"`
	Type     sdk.FieldType `toml:"type"     json:"type"`
	Required bool          `toml:"required" json:"required"`
	Default  string        `toml:"default"  json:"default,omitempty"`
}

// ErrManifestMismatch is returned when manifest.name disagrees with the
// directory name. Callers usually log + skip the plugin.
var ErrManifestMismatch = errors.New("plugin: manifest name does not match directory")

// LoadManifest reads + parses dir/manifest.toml and sanity-checks the
// resulting struct. It does NOT verify api_version against the running
// host — the host does that explicitly so a stale plugin still shows up
// in the settings UI with a useful error message.
func LoadManifest(dir string) (*Manifest, error) {
	path := filepath.Join(dir, "manifest.toml")
	data, err := os.ReadFile(path) // #nosec G304 -- dir is the host-resolved plugins dir.
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var m Manifest
	if err := toml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	m.Name = strings.TrimSpace(m.Name)
	if m.Name == "" {
		return nil, errors.New("manifest: name is required")
	}
	expected := strings.TrimSpace(filepath.Base(dir))
	if !strings.EqualFold(m.Name, expected) {
		return nil, fmt.Errorf("%w: manifest=%q dir=%q", ErrManifestMismatch, m.Name, expected)
	}
	if m.APIVersion <= 0 {
		return nil, errors.New("manifest: api_version is required")
	}
	if m.ConfigSchema.Fields == nil {
		m.ConfigSchema.Fields = map[string]ManifestField{}
	}
	for key, f := range m.ConfigSchema.Fields {
		if !sdk.IsValidFieldType(f.Type) {
			return nil, fmt.Errorf("manifest: field %q has unknown type %q (want one of: %s)",
				key, f.Type, sdk.SupportedFieldTypes())
		}
	}
	return &m, nil
}

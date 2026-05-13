package plugin

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dusthoff/hashpoint/internal/plugin/sdk"
)

func TestLoadManifest_AcceptsKnownFieldTypes(t *testing.T) {
	dir := writeManifest(t, "oncall-jira", `
name = "oncall-jira"
version = "0.1.0"
api_version = 1
description = "Files Jira tickets for off-duty work"
capabilities = ["oncall_documentation"]

[config_schema.fields.endpoint]
label = "Jira base URL"
type = "text"
required = true

[config_schema.fields.api_token]
label = "API token"
type = "password"
required = true

[config_schema.fields.dry_run]
label = "Dry run"
type = "boolean"
required = false
default = "false"
`)
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if got := m.ConfigSchema.Fields["endpoint"].Type; got != sdk.FieldTypeText {
		t.Errorf("endpoint type: got %q want %q", got, sdk.FieldTypeText)
	}
	if got := m.ConfigSchema.Fields["api_token"].Type; got != sdk.FieldTypePassword {
		t.Errorf("api_token type: got %q want %q", got, sdk.FieldTypePassword)
	}
	if got := m.ConfigSchema.Fields["dry_run"].Type; got != sdk.FieldTypeBool {
		t.Errorf("dry_run type: got %q want %q", got, sdk.FieldTypeBool)
	}
}

func TestLoadManifest_RejectsUnknownFieldType(t *testing.T) {
	dir := writeManifest(t, "weird", `
name = "weird"
version = "0.0.1"
api_version = 1

[config_schema.fields.foo]
label = "Foo"
type = "magic"
`)
	_, err := LoadManifest(dir)
	if err == nil {
		t.Fatalf("expected error for unknown field type, got nil")
	}
	if !strings.Contains(err.Error(), "magic") || !strings.Contains(err.Error(), "foo") {
		t.Errorf("error should mention the offending key + type, got %q", err)
	}
}

func TestLoadManifest_RejectsNameMismatch(t *testing.T) {
	dir := writeManifest(t, "actual-dir", `
name = "different-name"
api_version = 1
`)
	_, err := LoadManifest(dir)
	if !errors.Is(err, ErrManifestMismatch) {
		t.Fatalf("want ErrManifestMismatch, got %v", err)
	}
}

func TestLoadManifest_AcceptsEmptyConfigSchema(t *testing.T) {
	dir := writeManifest(t, "minimal", `
name = "minimal"
version = "0.0.1"
api_version = 1
`)
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m.ConfigSchema.Fields == nil {
		t.Fatalf("expected non-nil Fields map, got nil")
	}
	if len(m.ConfigSchema.Fields) != 0 {
		t.Fatalf("expected zero fields, got %d", len(m.ConfigSchema.Fields))
	}
}

// writeManifest creates <tmp>/<name>/manifest.toml with body and returns
// the plugin directory path that LoadManifest accepts.
func writeManifest(t *testing.T, name, body string) string {
	t.Helper()
	root := t.TempDir()
	pluginDir := filepath.Join(root, name)
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "manifest.toml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return pluginDir
}

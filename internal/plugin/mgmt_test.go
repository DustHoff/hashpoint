package plugin

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/dusthoff/hashpoint/plugin/sdk"
)

// fakeSettings is an in-memory SettingsStore for tests. Only the
// surface the mgmt code uses is exercised; uninvoked methods return
// zeroes.
type fakeSettings struct {
	mu        sync.Mutex
	clearedAt []string
	plain     map[string]map[string]string
	secrets   map[string]map[string]string
	enabled   map[string]bool
}

func newFakeSettings() *fakeSettings {
	return &fakeSettings{
		plain:   map[string]map[string]string{},
		secrets: map[string]map[string]string{},
		enabled: map[string]bool{},
	}
}

func (f *fakeSettings) GetEnabled(_ context.Context, name string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.enabled[name]
	if !ok {
		return true, nil
	}
	return v, nil
}
func (f *fakeSettings) SetEnabled(_ context.Context, name string, v bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enabled[name] = v
	return nil
}
func (f *fakeSettings) GetFields(_ context.Context, name string) (map[string]string, map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	plain := map[string]string{}
	for k, v := range f.plain[name] {
		plain[k] = v
	}
	secrets := map[string]string{}
	for k, v := range f.secrets[name] {
		secrets[k] = v
	}
	return plain, secrets, nil
}
func (f *fakeSettings) GetSecret(_ context.Context, name, key string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.secrets[name][key]
	return v, ok, nil
}
func (f *fakeSettings) SetField(_ context.Context, name, key, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.plain[name] == nil {
		f.plain[name] = map[string]string{}
	}
	f.plain[name][key] = value
	return nil
}
func (f *fakeSettings) SetSecretField(_ context.Context, name, key, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.secrets[name] == nil {
		f.secrets[name] = map[string]string{}
	}
	f.secrets[name][key] = value
	return nil
}
func (f *fakeSettings) DeleteField(_ context.Context, name, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.plain[name], key)
	delete(f.secrets[name], key)
	return nil
}
func (f *fakeSettings) Clear(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clearedAt = append(f.clearedAt, name)
	delete(f.plain, name)
	delete(f.secrets, name)
	delete(f.enabled, name)
	return nil
}

// fakeMgmt records every call so tests can assert routing.
type fakeMgmt struct {
	mu sync.Mutex

	available []sdk.AvailablePlugin
	listErr   error

	installed   []string
	updated     []string
	uninstalled []string

	// onCall is invoked at the start of Install/Update/Uninstall so a
	// test can inspect host state at the moment the handler is called.
	// Useful for proving "stopAndForget happened before handler.Update".
	onCall func(method, name string)
}

func (f *fakeMgmt) ListAvailable(_ context.Context) ([]sdk.AvailablePlugin, error) {
	return f.available, f.listErr
}
func (f *fakeMgmt) Install(_ context.Context, name string) error {
	if f.onCall != nil {
		f.onCall("install", name)
	}
	f.mu.Lock()
	f.installed = append(f.installed, name)
	f.mu.Unlock()
	return nil
}
func (f *fakeMgmt) Update(_ context.Context, name string) error {
	if f.onCall != nil {
		f.onCall("update", name)
	}
	f.mu.Lock()
	f.updated = append(f.updated, name)
	f.mu.Unlock()
	return nil
}
func (f *fakeMgmt) Uninstall(_ context.Context, name string) error {
	if f.onCall != nil {
		f.onCall("uninstall", name)
	}
	f.mu.Lock()
	f.uninstalled = append(f.uninstalled, name)
	f.mu.Unlock()
	return nil
}

// quietHost builds a Host that does not log anywhere — keeps test output clean.
func quietHost(t *testing.T, settings SettingsStore, pluginsDir string) *Host {
	t.Helper()
	return NewHost(HostDeps{
		Logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		PluginsDir:        pluginsDir,
		Settings:          settings,
		DiscoveryInterval: -1, // disabled in tests
	})
}

// withRunningSource preinstalls a fake running source plugin into the
// host so the mgmt routing layer can find it.
func withRunningSource(h *Host, name string, mgmt sdk.PluginManagementHandler, version string) {
	h.plugins[name] = &pluginInstance{
		name:     name,
		state:    StateRunning,
		mgmt:     mgmt,
		manifest: &Manifest{Name: name, Version: version},
		meta:     sdk.Metadata{Name: name, Version: version},
	}
}

func TestUninstallPlugin_RefusesSelf(t *testing.T) {
	settings := newFakeSettings()
	h := quietHost(t, settings, t.TempDir())
	mgmt := &fakeMgmt{}
	withRunningSource(h, "source", mgmt, "1.0.0")

	err := h.UninstallPlugin(context.Background(), "source", "source")
	if !errors.Is(err, ErrSelfUninstallRefused) {
		t.Fatalf("want ErrSelfUninstallRefused, got %v", err)
	}
	if len(mgmt.uninstalled) != 0 {
		t.Errorf("handler.Uninstall must not be called on self-uninstall, got %v", mgmt.uninstalled)
	}
	if len(settings.clearedAt) != 0 {
		t.Errorf("settings.Clear must not be called on self-uninstall, got %v", settings.clearedAt)
	}
}

func TestUninstallPlugin_HappyPath(t *testing.T) {
	settings := newFakeSettings()
	h := quietHost(t, settings, t.TempDir())
	mgmt := &fakeMgmt{}
	withRunningSource(h, "source", mgmt, "1.0.0")

	// Pretend the target is installed: leave a running pluginInstance + some persisted config.
	h.plugins["target"] = &pluginInstance{name: "target", state: StateRunning}
	_ = settings.SetField(context.Background(), "target", "endpoint", "https://example.com")

	if err := h.UninstallPlugin(context.Background(), "source", "target"); err != nil {
		t.Fatalf("UninstallPlugin: %v", err)
	}
	if got := mgmt.uninstalled; len(got) != 1 || got[0] != "target" {
		t.Errorf("handler.Uninstall calls: %v", got)
	}
	if got := settings.clearedAt; len(got) != 1 || got[0] != "target" {
		t.Errorf("settings.Clear calls: %v", got)
	}
	if _, ok := h.plugins["target"]; ok {
		t.Errorf("target still present in host registry")
	}
}

func TestUninstallPlugin_UnknownSourceError(t *testing.T) {
	settings := newFakeSettings()
	h := quietHost(t, settings, t.TempDir())

	err := h.UninstallPlugin(context.Background(), "missing-source", "target")
	if !errors.Is(err, ErrUnknownPluginSource) {
		t.Fatalf("want ErrUnknownPluginSource, got %v", err)
	}
}

func TestListAvailablePlugins_MergesSources(t *testing.T) {
	h := quietHost(t, newFakeSettings(), t.TempDir())

	mgmtA := &fakeMgmt{available: []sdk.AvailablePlugin{
		{Name: "oncall-jira", Version: "1.0.0", Description: "Jira"},
	}}
	mgmtB := &fakeMgmt{available: []sdk.AvailablePlugin{
		{Name: "oncall-otrs", Version: "0.2.0", Description: "OTRS"},
	}}
	withRunningSource(h, "source-a", mgmtA, "1.0.0")
	withRunningSource(h, "source-b", mgmtB, "1.0.0")
	// Pretend oncall-jira v0.9.0 is already installed.
	h.plugins["oncall-jira"] = &pluginInstance{
		name:     "oncall-jira",
		state:    StateRunning,
		manifest: &Manifest{Name: "oncall-jira", Version: "0.9.0"},
		meta:     sdk.Metadata{Name: "oncall-jira", Version: "0.9.0"},
	}

	out, err := h.ListAvailablePlugins(context.Background())
	if err != nil {
		t.Fatalf("ListAvailablePlugins: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("want 2 entries, got %d", len(out))
	}
	jira, otrs := out[0], out[1]
	if jira.Name != "oncall-jira" {
		t.Errorf("sort order: first row should be oncall-jira, got %q", jira.Name)
	}
	if jira.InstalledVersion != "0.9.0" {
		t.Errorf("InstalledVersion mismatch: %q", jira.InstalledVersion)
	}
	if jira.SourcePlugin != "source-a" {
		t.Errorf("SourcePlugin mismatch: %q", jira.SourcePlugin)
	}
	if otrs.InstalledVersion != "" {
		t.Errorf("uninstalled plugin should have empty InstalledVersion, got %q", otrs.InstalledVersion)
	}
}

func TestListAvailablePlugins_NoSourcesReturnsNil(t *testing.T) {
	h := quietHost(t, newFakeSettings(), t.TempDir())
	out, err := h.ListAvailablePlugins(context.Background())
	if err != nil {
		t.Fatalf("ListAvailablePlugins: %v", err)
	}
	if out != nil {
		t.Errorf("expected nil, got %v", out)
	}
}

func TestUpdatePlugin_StopsTargetBeforeHandlerCall(t *testing.T) {
	settings := newFakeSettings()
	h := quietHost(t, settings, t.TempDir())

	// Source plugin advertises mgmt and records what host state looks
	// like when Update fires. We expect h.plugins["target"] to be gone
	// by then.
	var targetVisibleAtUpdate bool
	mgmt := &fakeMgmt{
		onCall: func(method, name string) {
			if method == "update" && name == "target" {
				h.mu.RLock()
				_, targetVisibleAtUpdate = h.plugins["target"]
				h.mu.RUnlock()
			}
		},
	}
	withRunningSource(h, "source", mgmt, "1.0.0")
	h.plugins["target"] = &pluginInstance{name: "target", state: StateRunning}

	// UpdatePlugin will fail at launch() because no binary exists, but
	// that only affects the post-handler state — the assertion below
	// is about the moment handler.Update was invoked.
	_ = h.UpdatePlugin(context.Background(), "source", "target")

	if targetVisibleAtUpdate {
		t.Errorf("target was still in host registry when handler.Update was called")
	}
	if got := mgmt.updated; len(got) != 1 || got[0] != "target" {
		t.Errorf("handler.Update calls: %v", got)
	}
}

func TestInstallPlugin_RejectsDuplicate(t *testing.T) {
	settings := newFakeSettings()
	h := quietHost(t, settings, t.TempDir())
	mgmt := &fakeMgmt{}
	withRunningSource(h, "source", mgmt, "1.0.0")
	h.plugins["target"] = &pluginInstance{name: "target", state: StateRunning}

	err := h.InstallPlugin(context.Background(), "source", "target")
	if err == nil {
		t.Fatalf("expected error for duplicate install")
	}
	if len(mgmt.installed) != 0 {
		t.Errorf("handler.Install must not be called on duplicate, got %v", mgmt.installed)
	}
}

func TestDiscoverNew_PicksUpFreshDirectories(t *testing.T) {
	settings := newFakeSettings()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "newcomer"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	var notified []string
	h := NewHost(HostDeps{
		Logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		PluginsDir:        dir,
		Settings:          settings,
		DiscoveryInterval: -1,
		OnDiscovered: func(info Info) {
			notified = append(notified, info.Name)
		},
	})

	h.discoverNew(context.Background())
	if _, ok := h.plugins["newcomer"]; !ok {
		t.Errorf("expected newcomer to be tracked after discovery, got: %v", h.plugins)
	}
	if len(notified) != 1 || notified[0] != "newcomer" {
		t.Errorf("OnDiscovered notifications: %v", notified)
	}

	// Second tick must not re-launch the already-known entry.
	prevState := h.plugins["newcomer"]
	h.discoverNew(context.Background())
	if h.plugins["newcomer"] != prevState {
		t.Errorf("discoverNew must not replace known entries on second tick")
	}
}

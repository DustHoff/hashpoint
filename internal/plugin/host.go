package plugin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	stdlog "log"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/dusthoff/hashpoint/plugin/sdk"
	hclog "github.com/hashicorp/go-hclog"
	hplugin "github.com/hashicorp/go-plugin"
)

// State mirrors the lifecycle of a discovered plugin from the
// host's perspective. The settings UI renders one row per plugin keyed
// off State + LastError.
type State string

const (
	// StateRunning means the plugin process is alive and Configure
	// succeeded — it can serve any capability it advertises.
	StateRunning State = "running"
	// StateNeedsConfig means the plugin's manifest declares one or more
	// required fields that the user has not yet filled in. The subprocess
	// is never started in this state; capability fan-outs skip the
	// plugin. Filling the missing fields + saving triggers a Reload
	// which can promote the plugin to StateRunning.
	StateNeedsConfig State = "needs_config"
	// StateFailed means we tried to launch / init / configure the plugin
	// and something went wrong. LastError carries the cause.
	StateFailed State = "failed"
	// StateDisabled means the user toggled the plugin off via the
	// settings UI. The subprocess is not running and capability fan-outs
	// skip it. The enable flag is persisted in plugin_state and survives
	// an app restart.
	StateDisabled State = "disabled"
)

// Info is the read-model the settings UI sees. Returned from
// Host.List(); never holds RPC handles.
type Info struct {
	Name          string               `json:"name"`
	Version       string               `json:"version"`
	Description   string               `json:"description"`
	Capabilities  []sdk.Capability     `json:"capabilities"`
	State         State                `json:"state"`
	LastError     string               `json:"last_error,omitempty"`
	Enabled       bool                 `json:"enabled"`
	MissingFields []string             `json:"missing_fields,omitempty"`
	ConfigSchema  ManifestConfigSchema `json:"config_schema"`
}

// SettingsStore is the persistence surface the host needs for per-plugin
// configuration and the enable flag. Satisfied at runtime by
// storage.PluginSettingsRepo; tests can inject an in-memory fake.
//
// Decoupling via a local interface keeps internal/plugin independent of
// internal/storage — the host doesn't care how settings are stored, just
// that it can read/write them.
type SettingsStore interface {
	GetEnabled(ctx context.Context, name string) (bool, error)
	SetEnabled(ctx context.Context, name string, enabled bool) error
	GetFields(ctx context.Context, name string) (plain map[string]string, secrets map[string]string, err error)
	GetSecret(ctx context.Context, name, key string) (value string, found bool, err error)
	SetField(ctx context.Context, name, key, value string) error
	SetSecretField(ctx context.Context, name, key, value string) error
	DeleteField(ctx context.Context, name, key string) error
	// Clear removes every row (settings + state) for the plugin. Used
	// by UninstallPlugin after the source handler has wiped the files.
	Clear(ctx context.Context, name string) error
}

// HostDeps wires the Host to its surrounding environment.
type HostDeps struct {
	Logger     *slog.Logger
	PluginsDir string
	Settings   SettingsStore
	// SubmitTimeout caps each per-plugin Submit() call during
	// SubmitOnCallDoc fan-out. Zero ⇒ defaultSubmitTimeout.
	SubmitTimeout time.Duration
	// DiscoveryInterval is how often the host re-scans PluginsDir for
	// freshly-installed plugin directories. Zero ⇒ defaultDiscoveryInterval
	// (30 s). Negative ⇒ disabled (useful in tests so the discovery loop
	// does not interfere with deterministic launch/uninstall scripting).
	DiscoveryInterval time.Duration
	// OnDiscovered is invoked once per plugin newly picked up by the
	// discovery loop, after launch() returns. The host calls it from a
	// background goroutine; the App layer typically forwards it to the
	// Wails "plugins:discovered" event so the frontend can refresh
	// without a manual reload. Nil ⇒ no notification.
	OnDiscovered func(Info)
}

const defaultSubmitTimeout = 30 * time.Second

// Host owns the lifecycle of every installed plugin. Methods on *Host are
// safe to call concurrently.
type Host struct {
	deps HostDeps
	log  *slog.Logger

	mu      sync.RWMutex
	plugins map[string]*pluginInstance

	handles *handleRegistry

	// discoveryCancel stops the periodic discovery goroutine. Set by
	// Start() when DiscoveryInterval >= 0; called by Stop().
	discoveryCancel context.CancelFunc
}

// pluginInstance is the host's internal view of one discovered plugin.
// State transitions go through Host methods only — never mutate fields
// directly outside of host.go.
type pluginInstance struct {
	name     string
	manifest *Manifest

	state   State
	lastErr string
	missing []string // populated only when state == StateNeedsConfig

	// running-only fields (nil when state != StateRunning)
	client    *hplugin.Client
	rpcClient hplugin.ClientProtocol
	core      sdk.Plugin
	meta      sdk.Metadata
	onCall    sdk.OnCallDocumentationHandler
	mgmt      sdk.PluginManagementHandler
}

// ErrNoOnCallPlugin is returned by SubmitOnCallDoc when no running plugin
// advertises CapOnCallDocumentation. The caller (App layer) treats this
// as "doc stays in draft state" per product decision — no error surfaces
// to the user.
var ErrNoOnCallPlugin = errors.New("plugin: no oncall_documentation plugin available")

// NewHost wires a host from its dependencies. Logging defaults to the
// global slog handler when deps.Logger is nil. The host does NOT launch
// any plugins until Start() is called.
func NewHost(deps HostDeps) *Host {
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.SubmitTimeout == 0 {
		deps.SubmitTimeout = defaultSubmitTimeout
	}
	return &Host{
		deps:    deps,
		log:     deps.Logger.With("subsystem", "plugin"),
		plugins: map[string]*pluginInstance{},
		handles: newHandleRegistry(),
	}
}

// Start discovers every plugin directory under deps.PluginsDir. For each:
// if the persisted enabled flag is false the plugin is recorded in
// StateDisabled (manifest loaded for the UI, no subprocess); otherwise
// the launch handshake runs and the plugin lands in StateRunning,
// StateNeedsConfig, or StateFailed depending on the outcome. A failure
// on any one plugin never affects the others — the host catalogs the
// problem and moves on.
func (h *Host) Start(ctx context.Context) error {
	entries, err := os.ReadDir(h.deps.PluginsDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			h.log.Debug("plugins dir absent — no plugins installed",
				"path", h.deps.PluginsDir)
			return nil
		}
		return fmt.Errorf("read plugins dir: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		enabled, err := h.deps.Settings.GetEnabled(ctx, name)
		if err != nil {
			h.log.Warn("read plugin enabled failed; treating as enabled",
				"name", name, "err", err)
			enabled = true
		}
		if !enabled {
			h.recordDisabled(name)
			continue
		}
		if err := h.launch(ctx, name); err != nil {
			h.log.Warn("plugin launch failed",
				"name", name, "err", err)
		}
	}

	interval := h.deps.DiscoveryInterval
	if interval == 0 {
		interval = defaultDiscoveryInterval
	}
	if interval > 0 {
		discoveryCtx, cancel := context.WithCancel(context.Background())
		h.discoveryCancel = cancel
		h.startDiscoveryLoop(discoveryCtx, interval)
	}
	return nil
}

// Stop kills every running plugin subprocess. Idempotent. Used at app
// shutdown — after Stop the host is unusable.
func (h *Host) Stop(_ context.Context) error {
	if h.discoveryCancel != nil {
		h.discoveryCancel()
		h.discoveryCancel = nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for name, p := range h.plugins {
		if p.client != nil {
			p.client.Kill()
		}
		h.handles.revokeFor(name)
		p.client = nil
		p.rpcClient = nil
		p.core = nil
		p.onCall = nil
		p.mgmt = nil
	}
	return nil
}

// Reload tears down the named plugin's subprocess (if running) and
// re-evaluates from scratch: persisted enable flag, manifest, required
// fields, then either recordDisabled, recordNeedsConfig, or a fresh
// launch. Used after SetConfig / SetSecret / SetEnabled.
func (h *Host) Reload(ctx context.Context, name string) error {
	h.mu.Lock()
	if p, ok := h.plugins[name]; ok {
		if p.client != nil {
			p.client.Kill()
		}
		h.handles.revokeFor(name)
		delete(h.plugins, name)
	}
	h.mu.Unlock()

	enabled, err := h.deps.Settings.GetEnabled(ctx, name)
	if err != nil {
		return fmt.Errorf("read enabled: %w", err)
	}
	if !enabled {
		h.recordDisabled(name)
		return nil
	}
	return h.launch(ctx, name)
}

// List returns the read-model the settings UI consumes. Sorted by Name
// for stable display.
func (h *Host) List() []Info {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]Info, 0, len(h.plugins))
	for _, p := range h.plugins {
		out = append(out, p.info())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Get returns one plugin's read-model.
func (h *Host) Get(name string) (Info, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	p, ok := h.plugins[name]
	if !ok {
		return Info{}, false
	}
	return p.info(), true
}

// SubmitResult carries one plugin's response from a fan-out.
type SubmitResult struct {
	PluginName string
	Result     sdk.SubmissionResult
	Err        error
}

// SubmitOnCallDoc dispatches doc to every running plugin advertising
// CapOnCallDocumentation. Plugins in any non-running state (disabled,
// needs_config, failed) are silently skipped — they cannot serve the
// capability. Each plugin gets its own goroutine with a per-plugin
// timeout (deps.SubmitTimeout). The sink is invoked once per plugin
// in arbitrary order.
//
// Returns ErrNoOnCallPlugin when no plugin can take the document — the
// caller treats that as a no-op (doc stays in draft state) per product
// decision; it is NOT a fatal error.
func (h *Host) SubmitOnCallDoc(ctx context.Context, doc sdk.OnCallDocument, sink func(SubmitResult)) error {
	type target struct {
		name    string
		handler sdk.OnCallDocumentationHandler
	}
	var targets []target

	h.mu.RLock()
	for name, p := range h.plugins {
		if p.state == StateRunning && p.onCall != nil {
			targets = append(targets, target{name: name, handler: p.onCall})
		}
	}
	h.mu.RUnlock()

	if len(targets) == 0 {
		return ErrNoOnCallPlugin
	}

	var wg sync.WaitGroup
	for _, t := range targets {
		wg.Add(1)
		go func(t target) {
			defer wg.Done()
			callCtx, cancel := context.WithTimeout(ctx, h.deps.SubmitTimeout)
			defer cancel()
			res, err := t.handler.Submit(callCtx, doc)
			sink(SubmitResult{PluginName: t.name, Result: res, Err: err})
		}(t)
	}
	wg.Wait()
	return nil
}

// SetEnabled persists the user-controlled enable flag and immediately
// applies it: enabling launches the plugin (or moves it to needs_config
// when required fields are missing); disabling stops the subprocess.
func (h *Host) SetEnabled(ctx context.Context, name string, enabled bool) error {
	if err := h.deps.Settings.SetEnabled(ctx, name, enabled); err != nil {
		return fmt.Errorf("persist enabled: %w", err)
	}
	return h.Reload(ctx, name)
}

// SetConfig replaces the plain (text + boolean) fields for the plugin
// with the supplied map, then reloads. Password fields are managed
// separately via SetSecret / DeleteSecret — values supplied here for
// password keys are silently ignored. Keys declared in the manifest but
// absent from the supplied map are deleted from the store (the user
// cleared the field).
func (h *Host) SetConfig(ctx context.Context, name string, fields map[string]string) error {
	h.mu.RLock()
	inst, ok := h.plugins[name]
	h.mu.RUnlock()
	if !ok || inst.manifest == nil {
		return fmt.Errorf("plugin %q not loaded", name)
	}
	for key, f := range inst.manifest.ConfigSchema.Fields {
		if f.Type == sdk.FieldTypePassword {
			continue
		}
		if val, present := fields[key]; present {
			if err := h.deps.Settings.SetField(ctx, name, key, val); err != nil {
				return err
			}
		} else {
			if err := h.deps.Settings.DeleteField(ctx, name, key); err != nil {
				return err
			}
		}
	}
	return h.Reload(ctx, name)
}

// SetSecret persists a single password-typed field (encrypted at rest)
// and reloads the plugin so the new value takes effect. Empty values
// are treated as "leave the existing secret alone" — to clear a secret,
// call DeleteSecret.
func (h *Host) SetSecret(ctx context.Context, name, key, value string) error {
	if value == "" {
		return h.Reload(ctx, name)
	}
	if err := h.deps.Settings.SetSecretField(ctx, name, key, value); err != nil {
		return fmt.Errorf("persist secret: %w", err)
	}
	return h.Reload(ctx, name)
}

// DeleteSecret removes a password-typed field and reloads.
func (h *Host) DeleteSecret(ctx context.Context, name, key string) error {
	if err := h.deps.Settings.DeleteField(ctx, name, key); err != nil {
		return fmt.Errorf("delete secret: %w", err)
	}
	return h.Reload(ctx, name)
}

// --- launch path --------------------------------------------------------

func (h *Host) launch(ctx context.Context, name string) error {
	// Skip launch when the plugin is already known to the host. Callers
	// that want a fresh launch (Reload / Update) explicitly drop the
	// entry from h.plugins first; everything else (discovery loop,
	// post-install launch) is happy with the current state.
	h.mu.RLock()
	_, exists := h.plugins[name]
	h.mu.RUnlock()
	if exists {
		return nil
	}

	dir := filepath.Join(h.deps.PluginsDir, name)
	man, err := LoadManifest(dir)
	if err != nil {
		h.recordFailure(name, nil, err)
		return err
	}
	if man.APIVersion != sdk.HostAPIVersion {
		e := fmt.Errorf("manifest api_version %d != host %d", man.APIVersion, sdk.HostAPIVersion)
		h.recordFailure(name, man, e)
		return e
	}

	cfg, missing, err := h.buildConfig(ctx, name, man)
	if err != nil {
		h.recordFailure(name, man, fmt.Errorf("build config: %w", err))
		return err
	}
	if len(missing) > 0 {
		h.recordNeedsConfig(name, man, missing)
		h.log.Info("plugin parked in needs_config",
			"name", name, "missing", missing)
		return nil
	}

	binPath := filepath.Join(dir, pluginBinaryName(name))
	if _, err := os.Stat(binPath); err != nil {
		e := fmt.Errorf("plugin binary missing: %s", binPath)
		h.recordFailure(name, man, e)
		return e
	}

	client := hplugin.NewClient(&hplugin.ClientConfig{
		HandshakeConfig:  sdk.Handshake,
		Plugins:          sdk.HostSidePluginMap(),
		Cmd:              exec.Command(binPath), // #nosec G204 -- binPath is built from the host-controlled plugins dir.
		AllowedProtocols: []hplugin.Protocol{hplugin.ProtocolNetRPC},
		Logger:           newHclogAdapter(h.log.With("plugin", name)),
	})

	rpcClient, err := client.Client()
	if err != nil {
		client.Kill()
		e := fmt.Errorf("dial plugin: %w", err)
		h.recordFailure(name, man, e)
		return e
	}

	rawCore, err := rpcClient.Dispense(sdk.CoreKey)
	if err != nil {
		client.Kill()
		e := fmt.Errorf("dispense core: %w", err)
		h.recordFailure(name, man, e)
		return e
	}
	core, ok := rawCore.(sdk.Plugin)
	if !ok {
		client.Kill()
		e := fmt.Errorf("core plugin: unexpected type %T", rawCore)
		h.recordFailure(name, man, e)
		return e
	}

	api := &boundHostAPI{
		pluginName: name,
		log:        h.log.With("plugin", name),
		handles:    h.handles,
		settings:   h.deps.Settings,
	}
	if err := core.Init(ctx, api); err != nil {
		client.Kill()
		e := fmt.Errorf("plugin Init: %w", err)
		h.recordFailure(name, man, e)
		return e
	}

	meta, err := core.Metadata(ctx)
	if err != nil {
		client.Kill()
		e := fmt.Errorf("plugin Metadata: %w", err)
		h.recordFailure(name, man, e)
		return e
	}
	if meta.APIVersion != sdk.HostAPIVersion {
		client.Kill()
		e := fmt.Errorf("plugin runtime api_version %d != host %d",
			meta.APIVersion, sdk.HostAPIVersion)
		h.recordFailure(name, man, e)
		return e
	}

	if err := core.Configure(ctx, cfg); err != nil {
		client.Kill()
		e := fmt.Errorf("plugin Configure: %w", err)
		h.recordFailure(name, man, e)
		return e
	}

	inst := &pluginInstance{
		name:      name,
		manifest:  man,
		state:     StateRunning,
		client:    client,
		rpcClient: rpcClient,
		core:      core,
		meta:      meta,
	}

	// Dispense capability-specific handlers if the plugin advertises them.
	for _, c := range meta.Capabilities {
		switch c {
		case sdk.CapOnCallDocumentation:
			raw, err := rpcClient.Dispense(sdk.OnCallKey)
			if err != nil {
				h.log.Warn("dispense oncall handler failed — capability disabled",
					"plugin", name, "err", err)
				continue
			}
			handler, ok := raw.(sdk.OnCallDocumentationHandler)
			if !ok {
				h.log.Warn("oncall handler: unexpected type",
					"plugin", name, "type", fmt.Sprintf("%T", raw))
				continue
			}
			inst.onCall = handler
		case sdk.CapPluginManagement:
			raw, err := rpcClient.Dispense(sdk.MgmtKey)
			if err != nil {
				h.log.Warn("dispense plugin_management handler failed — capability disabled",
					"plugin", name, "err", err)
				continue
			}
			handler, ok := raw.(sdk.PluginManagementHandler)
			if !ok {
				h.log.Warn("plugin_management handler: unexpected type",
					"plugin", name, "type", fmt.Sprintf("%T", raw))
				continue
			}
			inst.mgmt = handler
		default:
			h.log.Debug("plugin advertised unknown capability — ignoring",
				"plugin", name, "capability", c)
		}
	}

	h.mu.Lock()
	h.plugins[name] = inst
	h.mu.Unlock()

	h.log.Info("plugin loaded",
		"name", name,
		"version", meta.Version,
		"capabilities", meta.Capabilities)
	return nil
}

// recordFailure persists a plugin in StateFailed without keeping any RPC
// handles. Used at every error gate in launch().
func (h *Host) recordFailure(name string, man *Manifest, cause error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	inst := h.plugins[name]
	if inst == nil {
		inst = &pluginInstance{name: name}
		h.plugins[name] = inst
	}
	inst.manifest = man
	inst.state = StateFailed
	inst.lastErr = cause.Error()
	inst.missing = nil
	inst.client = nil
	inst.rpcClient = nil
	inst.core = nil
	inst.onCall = nil
	inst.mgmt = nil
}

// recordNeedsConfig parks a plugin in StateNeedsConfig with the list of
// required-but-unset field keys. No subprocess is alive in this state.
func (h *Host) recordNeedsConfig(name string, man *Manifest, missing []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	inst := h.plugins[name]
	if inst == nil {
		inst = &pluginInstance{name: name}
		h.plugins[name] = inst
	}
	inst.manifest = man
	inst.state = StateNeedsConfig
	inst.lastErr = ""
	inst.missing = append(inst.missing[:0], missing...)
	inst.client = nil
	inst.rpcClient = nil
	inst.core = nil
	inst.onCall = nil
	inst.mgmt = nil
}

// recordDisabled records a plugin in StateDisabled. The manifest is
// loaded on a best-effort basis so the settings UI can render the
// plugin's name and version even while it is off.
func (h *Host) recordDisabled(name string) {
	dir := filepath.Join(h.deps.PluginsDir, name)
	man, manErr := LoadManifest(dir)
	h.mu.Lock()
	defer h.mu.Unlock()
	inst := h.plugins[name]
	if inst == nil {
		inst = &pluginInstance{name: name}
		h.plugins[name] = inst
	}
	if manErr == nil {
		inst.manifest = man
		inst.lastErr = ""
	} else {
		inst.lastErr = fmt.Sprintf("manifest unreadable: %v", manErr)
	}
	inst.state = StateDisabled
	inst.missing = nil
	inst.client = nil
	inst.rpcClient = nil
	inst.core = nil
	inst.onCall = nil
	inst.mgmt = nil
}

// buildConfig assembles the PluginConfig delivered to Plugin.Configure
// and reports any required fields that are missing. The launch path
// short-circuits to StateNeedsConfig when missing is non-empty, so the
// subprocess is never started against an incomplete configuration.
//
// SecretHandles are minted only after the missing-fields check passes,
// to avoid leaking handles into the registry for plugins that will
// never reach StateRunning.
func (h *Host) buildConfig(ctx context.Context, name string, man *Manifest) (sdk.PluginConfig, []string, error) {
	plain, secrets, err := h.deps.Settings.GetFields(ctx, name)
	if err != nil {
		return sdk.PluginConfig{}, nil, fmt.Errorf("read plugin fields: %w", err)
	}

	plainOut := map[string]string{}
	var passwordKeys []string
	var missing []string

	for key, f := range man.ConfigSchema.Fields {
		if f.Type == sdk.FieldTypePassword {
			if _, ok := secrets[key]; !ok {
				if f.Required {
					missing = append(missing, key)
				}
				continue
			}
			passwordKeys = append(passwordKeys, key)
			continue
		}
		val, ok := plain[key]
		if !ok || val == "" {
			if f.Default != "" {
				val = f.Default
				ok = true
			}
		}
		if !ok {
			if f.Required {
				missing = append(missing, key)
			}
			continue
		}
		plainOut[key] = val
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		return sdk.PluginConfig{}, missing, nil
	}

	cfg := sdk.PluginConfig{
		Fields:  plainOut,
		Secrets: map[string]sdk.SecretHandle{},
	}
	for _, k := range passwordKeys {
		cfg.Secrets[k] = h.handles.mint(name, k)
	}
	return cfg, nil, nil
}

// info is the read-model projection of pluginInstance.
func (p *pluginInstance) info() Info {
	out := Info{
		Name:          p.name,
		State:         p.state,
		LastError:     p.lastErr,
		Enabled:       p.state != StateDisabled,
		MissingFields: append([]string(nil), p.missing...),
	}
	if p.manifest != nil {
		out.Version = p.manifest.Version
		out.Description = p.manifest.Description
		out.ConfigSchema = p.manifest.ConfigSchema
		out.Capabilities = make([]sdk.Capability, 0, len(p.manifest.Capabilities))
		for _, c := range p.manifest.Capabilities {
			out.Capabilities = append(out.Capabilities, sdk.Capability(c))
		}
	}
	// If the running runtime advertised richer metadata than the manifest,
	// prefer the runtime (the manifest is for the offline settings UI).
	if p.state == StateRunning {
		out.Version = p.meta.Version
		out.Description = p.meta.Description
		out.Capabilities = append(out.Capabilities[:0], p.meta.Capabilities...)
	}
	return out
}

// pluginBinaryName returns the expected binary file under <plugins>/<name>/.
// Windows: <name>.exe; everything else: <name>. Kept as a single function
// so a future "manifest declares binary name" feature is one edit.
func pluginBinaryName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// --- hclog adapter ------------------------------------------------------

// newHclogAdapter returns an hclog.Logger that bridges hashicorp/go-plugin's
// internal logging onto Hashpoint's slog handler. go-plugin writes a lot
// at Debug level (subprocess stdio, handshake details); the adapter
// promotes its Info+ entries unchanged and drops the rest.
//
// The adapter is intentionally minimal — we override just the level
// methods and inherit the rest from hclog's default null logger.
func newHclogAdapter(log *slog.Logger) hclog.Logger {
	return &hclogAdapter{log: log}
}

// hclogAdapter satisfies hashicorp/go-plugin's hclog.Logger surface by
// forwarding to a slog.Logger. Most methods are trivial level-mappings;
// SetLevel/StandardLogger are stubs because we never expose those knobs
// to plugins.
type hclogAdapter struct {
	log  *slog.Logger
	name string
}

// Log routes a leveled hclog message to the matching slog level.
func (a *hclogAdapter) Log(level hclog.Level, msg string, args ...interface{}) {
	switch level {
	case hclog.Debug, hclog.Trace, hclog.NoLevel:
		a.log.Debug(msg, args...)
	case hclog.Info:
		a.log.Info(msg, args...)
	case hclog.Warn:
		a.log.Warn(msg, args...)
	case hclog.Error:
		a.log.Error(msg, args...)
	default:
		a.log.Info(msg, args...)
	}
}

// Trace logs at slog.Debug — hclog's Trace level has no slog analogue.
func (a *hclogAdapter) Trace(msg string, args ...interface{}) { a.log.Debug(msg, args...) }

// Debug logs at slog.Debug.
func (a *hclogAdapter) Debug(msg string, args ...interface{}) { a.log.Debug(msg, args...) }

// Info logs at slog.Info.
func (a *hclogAdapter) Info(msg string, args ...interface{}) { a.log.Info(msg, args...) }

// Warn logs at slog.Warn.
func (a *hclogAdapter) Warn(msg string, args ...interface{}) { a.log.Warn(msg, args...) }

// Error logs at slog.Error.
func (a *hclogAdapter) Error(msg string, args ...interface{}) { a.log.Error(msg, args...) }

// IsTrace reports whether Trace messages are emitted. We mute trace.
func (a *hclogAdapter) IsTrace() bool { return false }

// IsDebug reports whether Debug messages are emitted.
func (a *hclogAdapter) IsDebug() bool { return true }

// IsInfo reports whether Info messages are emitted.
func (a *hclogAdapter) IsInfo() bool { return true }

// IsWarn reports whether Warn messages are emitted.
func (a *hclogAdapter) IsWarn() bool { return true }

// IsError reports whether Error messages are emitted.
func (a *hclogAdapter) IsError() bool { return true }

// ImpliedArgs returns the structured args carried by With (none here —
// hclog uses this for log enrichment, slog carries them on the Logger).
func (a *hclogAdapter) ImpliedArgs() []interface{} { return nil }

// With returns a child logger that prepends the given key/value pairs
// to every record, mirroring slog.Logger.With's contract.
func (a *hclogAdapter) With(args ...interface{}) hclog.Logger {
	return &hclogAdapter{log: a.log.With(args...), name: a.name}
}

// Name returns the logger's component name (set via Named/ResetNamed).
func (a *hclogAdapter) Name() string { return a.name }

// Named returns a logger tagged with name; hclog uses this to scope
// plugin output (e.g. "plugin.oncall-bridge"). We do not nest names
// because slog carries the same info as a structured attribute.
func (a *hclogAdapter) Named(name string) hclog.Logger {
	return &hclogAdapter{log: a.log, name: name}
}

// ResetNamed replaces the component name (vs. Named which appends).
func (a *hclogAdapter) ResetNamed(name string) hclog.Logger {
	return &hclogAdapter{log: a.log, name: name}
}

// SetLevel is a stub — log levels are fixed by the host slog handler.
func (a *hclogAdapter) SetLevel(_ hclog.Level) {}

// GetLevel returns hclog.Debug — see SetLevel.
func (a *hclogAdapter) GetLevel() hclog.Level { return hclog.Debug }

// StandardLogger returns a discarding stdlib *log.Logger; go-plugin only
// uses this for opaque, unstructured chatter we don't want to surface.
func (a *hclogAdapter) StandardLogger(_ *hclog.StandardLoggerOptions) *stdlog.Logger {
	return stdlog.New(io.Discard, "", 0)
}

// StandardWriter returns io.Discard — companion to StandardLogger.
func (a *hclogAdapter) StandardWriter(_ *hclog.StandardLoggerOptions) io.Writer {
	return io.Discard
}

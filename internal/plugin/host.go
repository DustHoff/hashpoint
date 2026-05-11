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

	hclog "github.com/hashicorp/go-hclog"
	hplugin "github.com/hashicorp/go-plugin"
	"github.com/onesi/hashpoint/internal/plugin/sdk"
)

// PluginState mirrors the lifecycle of a discovered plugin from the
// host's perspective. The settings UI renders one row per plugin keyed
// off State + LastError.
type PluginState string

const (
	// StateRunning means the plugin process is alive and Configure
	// succeeded — it can serve any capability it advertises.
	StateRunning PluginState = "running"
	// StateFailed means we tried to launch / init / configure the plugin
	// and something went wrong. LastError carries the cause.
	StateFailed PluginState = "failed"
	// StateDisabled is reserved for a future "user toggled this off"
	// switch. Not produced today.
	StateDisabled PluginState = "disabled"
)

// PluginInfo is the read-model the settings UI sees. Returned from
// Host.List(); never holds RPC handles.
type PluginInfo struct {
	Name         string               `json:"name"`
	Version      string               `json:"version"`
	Description  string               `json:"description"`
	Capabilities []sdk.Capability     `json:"capabilities"`
	State        PluginState          `json:"state"`
	LastError    string               `json:"last_error,omitempty"`
	ConfigSchema ManifestConfigSchema `json:"config_schema"`
}

// FieldsProvider is implemented by the App layer to feed per-plugin
// fields (non-secret config values) from config.toml [plugins.<name>]
// into the host without the host taking a direct dep on internal/config.
type FieldsProvider interface {
	// PluginFields returns a fresh copy of the field map for pluginName.
	// Returning nil is equivalent to returning an empty map.
	PluginFields(pluginName string) map[string]string
}

// HostDeps wires the Host to its surrounding environment.
type HostDeps struct {
	Logger     *slog.Logger
	PluginsDir string
	Secrets    SecretStore
	Fields     FieldsProvider
	// SubmitTimeout caps each per-plugin Submit() call during
	// SubmitOnCallDoc fan-out. Zero ⇒ defaultSubmitTimeout.
	SubmitTimeout time.Duration
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
}

// pluginInstance is the host's internal view of one running plugin.
// State transitions go through Host methods only — never mutate fields
// directly outside of host.go.
type pluginInstance struct {
	name     string
	manifest *Manifest

	state   PluginState
	lastErr string

	// running-only fields (nil when state != StateRunning)
	client    *hplugin.Client
	rpcClient hplugin.ClientProtocol
	core      sdk.Plugin
	meta      sdk.Metadata
	onCall    sdk.OnCallDocumentationHandler
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

// Start discovers every plugin directory under deps.PluginsDir, validates
// its manifest, launches the subprocess and walks the Init→Metadata→
// Configure handshake. A failure on any one plugin is logged and the
// plugin is recorded in StateFailed; Start returns nil unless something
// catastrophic happens (e.g. PluginsDir unreadable for a reason other
// than "missing").
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
		if err := h.launch(ctx, e.Name()); err != nil {
			h.log.Warn("plugin launch failed",
				"name", e.Name(), "err", err)
		}
	}
	return nil
}

// Stop kills every running plugin subprocess. Idempotent.
func (h *Host) Stop(_ context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	for name, p := range h.plugins {
		if p.client != nil {
			p.client.Kill()
		}
		h.handles.revokeFor(name)
		p.state = StateDisabled
		p.client = nil
		p.rpcClient = nil
		p.core = nil
		p.onCall = nil
	}
	return nil
}

// Reload tears down and re-launches a single plugin. Used after a config
// or secret change. Reloading a never-launched name attempts a fresh
// launch.
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
	return h.launch(ctx, name)
}

// List returns the read-model the settings UI consumes. Sorted by Name
// for stable display.
func (h *Host) List() []PluginInfo {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]PluginInfo, 0, len(h.plugins))
	for _, p := range h.plugins {
		out = append(out, p.info())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Get returns one plugin's read-model.
func (h *Host) Get(name string) (PluginInfo, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	p, ok := h.plugins[name]
	if !ok {
		return PluginInfo{}, false
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
// CapOnCallDocumentation. Each plugin gets its own goroutine with a
// per-plugin timeout (deps.SubmitTimeout). The sink is invoked once per
// plugin in arbitrary order.
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

// SetSecret persists a per-plugin secret. The plugin is NOT automatically
// reloaded — callers that need the new value to take effect must call
// Reload(name).
func (h *Host) SetSecret(name, key, value string) error {
	return h.deps.Secrets.Set(name, key, value)
}

// DeleteSecret removes a per-plugin secret. Same reload caveat as SetSecret.
func (h *Host) DeleteSecret(name, key string) error {
	return h.deps.Secrets.Delete(name, key)
}

// --- launch path --------------------------------------------------------

func (h *Host) launch(ctx context.Context, name string) error {
	dir := filepath.Join(h.deps.PluginsDir, name)
	man, err := LoadManifest(dir)
	if err != nil {
		h.recordFailure(name, nil, err)
		return err
	}
	if man.APIVersion != sdk.HostAPIVersion {
		err := fmt.Errorf("manifest api_version %d != host %d", man.APIVersion, sdk.HostAPIVersion)
		h.recordFailure(name, man, err)
		return err
	}
	binPath := filepath.Join(dir, pluginBinaryName(name))
	if _, err := os.Stat(binPath); err != nil {
		err := fmt.Errorf("plugin binary missing: %s", binPath)
		h.recordFailure(name, man, err)
		return err
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
		err := fmt.Errorf("dial plugin: %w", err)
		h.recordFailure(name, man, err)
		return err
	}

	rawCore, err := rpcClient.Dispense(sdk.CoreKey)
	if err != nil {
		client.Kill()
		err := fmt.Errorf("dispense core: %w", err)
		h.recordFailure(name, man, err)
		return err
	}
	core, ok := rawCore.(sdk.Plugin)
	if !ok {
		client.Kill()
		err := fmt.Errorf("core plugin: unexpected type %T", rawCore)
		h.recordFailure(name, man, err)
		return err
	}

	api := &boundHostAPI{
		pluginName: name,
		log:        h.log.With("plugin", name),
		handles:    h.handles,
		secrets:    h.deps.Secrets,
	}
	if err := core.Init(ctx, api); err != nil {
		client.Kill()
		err := fmt.Errorf("plugin Init: %w", err)
		h.recordFailure(name, man, err)
		return err
	}

	meta, err := core.Metadata(ctx)
	if err != nil {
		client.Kill()
		err := fmt.Errorf("plugin Metadata: %w", err)
		h.recordFailure(name, man, err)
		return err
	}
	if meta.APIVersion != sdk.HostAPIVersion {
		client.Kill()
		err := fmt.Errorf("plugin runtime api_version %d != host %d",
			meta.APIVersion, sdk.HostAPIVersion)
		h.recordFailure(name, man, err)
		return err
	}

	cfg := h.buildConfig(name, man)
	if err := core.Configure(ctx, cfg); err != nil {
		client.Kill()
		err := fmt.Errorf("plugin Configure: %w", err)
		h.recordFailure(name, man, err)
		return err
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
	inst.client = nil
	inst.rpcClient = nil
	inst.core = nil
	inst.onCall = nil
}

// buildConfig assembles the PluginConfig delivered to Plugin.Configure.
// Fields come from the user's config.toml [plugins.<name>] section
// (provided via FieldsProvider) overlaid on top of manifest defaults;
// Secrets are minted SecretHandles — one per secret key in the manifest.
func (h *Host) buildConfig(name string, man *Manifest) sdk.PluginConfig {
	fields := map[string]string{}
	if h.deps.Fields != nil {
		for k, v := range h.deps.Fields.PluginFields(name) {
			fields[k] = v
		}
	}
	for k, f := range man.ConfigSchema.Fields {
		if _, ok := fields[k]; !ok && f.Default != "" {
			fields[k] = f.Default
		}
	}
	secrets := map[string]sdk.SecretHandle{}
	for k := range man.ConfigSchema.Secrets {
		secrets[k] = h.handles.mint(name, k)
	}
	return sdk.PluginConfig{Fields: fields, Secrets: secrets}
}

// info is the read-model projection of pluginInstance.
func (p *pluginInstance) info() PluginInfo {
	out := PluginInfo{
		Name:      p.name,
		State:     p.state,
		LastError: p.lastErr,
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

type hclogAdapter struct {
	log  *slog.Logger
	name string
}

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

func (a *hclogAdapter) Trace(msg string, args ...interface{}) { a.log.Debug(msg, args...) }
func (a *hclogAdapter) Debug(msg string, args ...interface{}) { a.log.Debug(msg, args...) }
func (a *hclogAdapter) Info(msg string, args ...interface{})  { a.log.Info(msg, args...) }
func (a *hclogAdapter) Warn(msg string, args ...interface{})  { a.log.Warn(msg, args...) }
func (a *hclogAdapter) Error(msg string, args ...interface{}) { a.log.Error(msg, args...) }

func (a *hclogAdapter) IsTrace() bool { return false }
func (a *hclogAdapter) IsDebug() bool { return true }
func (a *hclogAdapter) IsInfo() bool  { return true }
func (a *hclogAdapter) IsWarn() bool  { return true }
func (a *hclogAdapter) IsError() bool { return true }

func (a *hclogAdapter) ImpliedArgs() []interface{} { return nil }
func (a *hclogAdapter) With(args ...interface{}) hclog.Logger {
	// Carry the args onto the embedded slog so hclog-style structured
	// logging shows up correctly.
	return &hclogAdapter{log: a.log.With(args...), name: a.name}
}
func (a *hclogAdapter) Name() string                   { return a.name }
func (a *hclogAdapter) Named(name string) hclog.Logger { return &hclogAdapter{log: a.log, name: name} }
func (a *hclogAdapter) ResetNamed(name string) hclog.Logger {
	return &hclogAdapter{log: a.log, name: name}
}
func (a *hclogAdapter) SetLevel(_ hclog.Level) {}
func (a *hclogAdapter) GetLevel() hclog.Level  { return hclog.Debug }
func (a *hclogAdapter) StandardLogger(_ *hclog.StandardLoggerOptions) *stdlog.Logger {
	return stdlog.New(io.Discard, "", 0)
}
func (a *hclogAdapter) StandardWriter(_ *hclog.StandardLoggerOptions) io.Writer {
	return io.Discard
}

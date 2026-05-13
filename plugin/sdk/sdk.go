// Package sdk defines the Hashpoint plugin contract: the Go interfaces a
// plugin author implements, the value types crossing the host↔plugin
// boundary, and the convenience helpers (Serve, PluginMap) that wire the
// implementation up to hashicorp/go-plugin's net/rpc transport.
//
// A plugin process is a separate executable Hashpoint launches as a
// subprocess. Communication goes over net/rpc multiplexed via yamux, so
// crashing a plugin never crashes the host. The transport is net/rpc
// rather than gRPC to keep the Hashpoint build pure-Go without a protoc
// toolchain; the SDK contract is transport-agnostic, so a future migration
// to gRPC requires only swapping the rpc_*.go wiring.
//
// Plugin author skeleton:
//
//	package main
//
//	import (
//	    "context"
//
//	    sdk "github.com/dusthoff/hashpoint/plugin/sdk"
//	)
//
//	type myPlugin struct {
//	    host        sdk.HostAPI
//	    endpoint    string
//	    tokenHandle sdk.SecretHandle
//	}
//
//	func (p *myPlugin) Init(ctx context.Context, host sdk.HostAPI) error {
//	    p.host = host
//	    return nil
//	}
//	func (p *myPlugin) Metadata(_ context.Context) (sdk.Metadata, error) {
//	    return sdk.Metadata{
//	        Name:         "oncall-example",
//	        Version:      "0.1.0",
//	        APIVersion:   sdk.HostAPIVersion,
//	        Capabilities: []sdk.Capability{sdk.CapOnCallDocumentation},
//	    }, nil
//	}
//	func (p *myPlugin) Configure(_ context.Context, cfg sdk.PluginConfig) error {
//	    p.endpoint = cfg.Fields["endpoint"]
//	    p.tokenHandle = cfg.Secrets["api_token"]
//	    return nil
//	}
//	func (p *myPlugin) Submit(ctx context.Context, doc sdk.OnCallDocument) (sdk.SubmissionResult, error) {
//	    token, err := p.host.RedeemSecret(ctx, p.tokenHandle)
//	    if err != nil { return sdk.SubmissionResult{}, err }
//	    // ... do something with token + doc ...
//	    _ = token
//	    return sdk.SubmissionResult{ExternalRef: "...", ExternalURL: "..."}, nil
//	}
//
//	func main() { sdk.Serve(&myPlugin{}) }
package sdk

import (
	"context"
	"errors"
	"time"

	hplugin "github.com/hashicorp/go-plugin"
)

// HostAPIVersion is bumped on every breaking change to the SDK surface
// (interface methods, wire types, handshake semantics). The host refuses
// to load plugins whose Metadata.APIVersion does not equal this value,
// avoiding silent ABI mismatch.
const HostAPIVersion = 1

// Handshake is the hashicorp/go-plugin handshake. Plugin binaries embed
// these constants via Serve(); a mismatch surfaces as an immediate launch
// failure rather than a subtle wire-format error later. Change
// MagicCookieValue to deliberately force every user to re-install plugins
// (e.g. after a breaking SDK change unrelated to ProtocolVersion).
var Handshake = hplugin.HandshakeConfig{
	ProtocolVersion:  1,
	MagicCookieKey:   "HASHPOINT_PLUGIN",
	MagicCookieValue: "v1-oncall-doc",
}

// Plugin-set keys. Both host and plugin must agree on these strings;
// adding a new capability adds a new key here AND a new entry in
// PluginMap / HostSidePluginMap.
const (
	pluginKeyCore           = "plugin"
	pluginKeyOnCall         = "oncall_documentation"
	pluginKeyMgmt           = "plugin_management"
	pluginKeyProcessAutoTag = "process_autotag"
)

// Capability is the string each plugin advertises in Metadata.Capabilities
// so the host knows which RPC services to dispense. A plugin may advertise
// multiple capabilities by implementing the corresponding Go interfaces.
type Capability string

// Capability values. Add new ones below — never re-use a retired string.
const (
	// CapOnCallDocumentation is advertised by plugins that implement
	// OnCallDocumentationHandler.
	CapOnCallDocumentation Capability = "oncall_documentation"
	// CapPluginManagement is advertised by plugins that implement
	// PluginManagementHandler. The host treats such a plugin as a
	// catalog source: it can list plugins available for install,
	// install/update them by writing files into PluginsDir, and
	// uninstall them by removing those files. The host orchestrates
	// the subprocess stop/start dance around mutating writes and the
	// DB cleanup around Uninstall — the handler is responsible only
	// for the bytes-on-disk side of things.
	CapPluginManagement Capability = "plugin_management"

	// CapProcessAutoTag is advertised by plugins that implement
	// ProcessAutoTagHandler. The host asks such plugins, once per focus
	// change (or comm-window change) on a process they have declared, to
	// supply a tag for an auto-tag-block. The plugin acts as a fallback
	// behind the user's hand-maintained rules: when no enabled rule
	// matches, the host consults every plugin that has declared the
	// focused process basename.
	CapProcessAutoTag Capability = "process_autotag"
)

// FieldType identifies the input element AND the persistence strategy
// for a config field declared in the manifest. Booleans are serialised
// as the strings "true" / "false" when delivered via PluginConfig.Fields.
// Password values never appear in PluginConfig.Fields — the host mints
// a SecretHandle for each password field and places it in
// PluginConfig.Secrets, so the cleartext stays out of the Configure
// payload and only crosses the wire when the plugin redeems it.
type FieldType string

// Supported FieldType values. Adding a new type requires host-side
// rendering support and an explicit branch in the persistence layer —
// keep the list closed and well-known.
const (
	FieldTypeText     FieldType = "text"
	FieldTypePassword FieldType = "password"
	FieldTypeBool     FieldType = "boolean"
)

// IsValidFieldType reports whether t is one of the supported FieldType
// values. The host uses this to reject manifests with unknown types
// at load time rather than silently degrading to "text".
func IsValidFieldType(t FieldType) bool {
	switch t {
	case FieldTypeText, FieldTypePassword, FieldTypeBool:
		return true
	}
	return false
}

// SupportedFieldTypes returns the legal FieldType values as a comma-
// separated string. Suitable for embedding in error messages produced
// by the manifest loader.
func SupportedFieldTypes() string {
	return string(FieldTypeText) + ", " + string(FieldTypePassword) + ", " + string(FieldTypeBool)
}

// Metadata is the plugin's self-description. Returned by Plugin.Metadata
// once per plugin lifetime (the host caches the result).
type Metadata struct {
	// Name must match the plugin's directory under PluginsDir. Used
	// everywhere the host needs to identify the plugin (logs, config
	// section, oncall_submissions.plugin_name).
	Name string
	// Version is informational, displayed in the settings UI.
	Version string
	// APIVersion MUST equal HostAPIVersion. The host refuses mismatches.
	APIVersion int
	// Capabilities lists the capability strings this plugin handles.
	// The host only dispenses RPC clients for capabilities advertised here.
	Capabilities []Capability
	// Description is the one-line text shown in the settings UI.
	Description string
}

// SecretHandle is an opaque pointer to a secret stored in the host's
// plugin_settings table (DPAPI-encrypted at rest, CurrentUser scope on
// Windows). The plaintext never crosses the host↔plugin boundary at
// Configure() time; the plugin redeems the handle via
// HostAPI.RedeemSecret() on-demand, holding the plaintext in memory
// only for the duration of an outbound call.
//
// Handles are per-plugin and per-process: leaking a handle from plugin A
// gives nothing useful to plugin B, and all handles die on host restart.
type SecretHandle string

// PluginConfig is what Configure() delivers.
//
// Fields contains the user-entered values for every manifest field
// whose type is "text" or "boolean" (booleans serialised as "true" /
// "false"). Secrets contains SecretHandles for fields whose type is
// "password" — the plugin redeems them lazily via HostAPI.RedeemSecret
// so the cleartext never crosses the Configure() payload. A field that
// the user has not filled in is absent from both maps (the plugin
// should detect this and return ErrNotConfigured if the value is
// required for its capability).
type PluginConfig struct {
	Fields  map[string]string
	Secrets map[string]SecretHandle
}

// HostAPI is the reverse-RPC surface the plugin uses to talk back into the
// host. Implementations live in the host process; the plugin receives a
// client stub via Init().
//
// Methods are kept narrow on purpose: anything richer should live in a
// capability-specific RPC, not in a "kitchen sink" host API.
type HostAPI interface {
	// RedeemSecret returns the plaintext for a SecretHandle the host
	// previously delivered in PluginConfig.Secrets. The plaintext is
	// in-memory only — callers must not write it to logs or disk.
	// Returns ErrUnknownSecretHandle for a stale / unrecognised handle.
	RedeemSecret(ctx context.Context, h SecretHandle) (string, error)

	// Log forwards a structured log line to the host's slog handler. The
	// host prefixes the plugin's Name automatically, so plugins should
	// not include their own name in the message. Levels: "debug", "info",
	// "warn", "error" (other strings degrade to info).
	Log(ctx context.Context, level, message string, fields map[string]string) error
}

// Plugin is the base interface every plugin must implement. The host
// invokes Init → Metadata → Configure in that order once on startup, then
// dispenses any capability-specific handlers the plugin advertises.
type Plugin interface {
	// Init delivers the HostAPI client. Plugins typically store it on the
	// receiver for later use. Called exactly once.
	Init(ctx context.Context, host HostAPI) error

	// Metadata is called once after Init. Must be cheap and side-effect
	// free — the host may call it before Configure to decide whether to
	// load the plugin at all.
	Metadata(ctx context.Context) (Metadata, error)

	// Configure delivers the user's per-plugin settings. Called once after
	// Metadata and again whenever the user saves new settings from the
	// settings UI. Plugins should return ErrConfigInvalid (wrapped with
	// fmt.Errorf("%w: detail", sdk.ErrConfigInvalid)) on validation
	// failures — the host surfaces the message verbatim.
	Configure(ctx context.Context, cfg PluginConfig) error
}

// IncidentType discriminates the two flavours the off-duty form supports.
type IncidentType string

// IncidentType values: planned maintenance versus unplanned service
// disruption. The host renders different form copy depending on which
// the user selects.
const (
	IncidentPlannedMaintenance IncidentType = "planned_maintenance"
	IncidentServiceDisruption  IncidentType = "service_disruption"
)

// OnCallDocument is the payload the host sends to OnCallDocumentationHandler
// plugins. Times are UTC. The plugin is responsible for idempotency on
// retry — use LocalID as a deduplication key when filing tickets remotely
// so a retried Submit does not create a duplicate.
type OnCallDocument struct {
	// LocalID is stable per Hashpoint document (UUID-shaped string).
	LocalID string
	// BlockID is the Hashpoint tag-block primary key. Useful for cross-
	// linking but the plugin should not assume it is stable across
	// database resets.
	BlockID int64
	// StartTime and EndTime span the off-duty work, UTC.
	StartTime time.Time
	EndTime   time.Time
	// TagName is the resolved display name (e.g. "#oncall/billing").
	TagName string
	// Application is the user-entered "which system was affected" field.
	Application string
	// IncidentType is the user's classification.
	IncidentType IncidentType
	// Solution is free-form text — the on-caller's notes.
	Solution string
}

// SubmissionResult is the plugin's response on success. Both fields are
// optional; the host displays ExternalRef as a chip in the inbox and
// ExternalURL as a clickable link.
type SubmissionResult struct {
	ExternalRef string
	ExternalURL string
}

// OnCallDocumentationHandler is implemented by plugins advertising
// CapOnCallDocumentation. The host fans Submit out to every running
// plugin advertising the capability; per-plugin results are tracked in
// the oncall_submissions table.
//
// Submit MUST be idempotent (use OnCallDocument.LocalID as a dedupe key on
// the remote side). Returning ErrTransient signals the host should keep
// the submission in 'failed' state and the user may retry; any other
// error is treated identically but surfaced verbatim in the UI.
type OnCallDocumentationHandler interface {
	Submit(ctx context.Context, doc OnCallDocument) (SubmissionResult, error)
}

// AvailablePlugin describes one entry in a plugin source's catalog. The
// host merges entries from every running PluginManagementHandler and
// stamps each row with the source plugin's name so Install/Update/
// Uninstall can be routed back to the originating handler. Name MUST
// be the value the would-be installed plugin will use for its directory
// and manifest — the host trusts the handler on this.
//
// JSON tags are present so the host can hand the type straight to the
// Wails layer; plugin authors typically construct values by name and
// never touch the JSON form.
type AvailablePlugin struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
}

// PluginManagementHandler is implemented by plugins advertising
// CapPluginManagement. They act as plugin sources: ListAvailable
// returns the catalog the user sees in the "Verfügbare Plugins" tab,
// and Install/Update/Uninstall mutate the bytes under PluginsDir.
//
// Contract with the host:
//
//   - Install/Update create or replace files under <PluginsDir>/<name>/
//     (binary + manifest.toml) and return nil on success.
//   - Update is invoked after the host has stopped the target plugin's
//     subprocess (Windows holds an exclusive lock on the running .exe),
//     so the handler can overwrite the binary in place.
//   - Uninstall removes <PluginsDir>/<name>/ from disk. The host clears
//     plugin_state + plugin_settings rows for the plugin after Uninstall
//     returns; the handler must not touch the database.
//   - All four methods MUST be safe to invoke concurrently with the
//     handler's own normal lifecycle (Init/Configure may run on the
//     handler at any time).
//
// Errors are surfaced verbatim in the UI; wrap with context so the user
// can understand what failed.
type PluginManagementHandler interface {
	ListAvailable(ctx context.Context) ([]AvailablePlugin, error)
	Install(ctx context.Context, name string) error
	Update(ctx context.Context, name string) error
	Uninstall(ctx context.Context, name string) error
}

// ProcessFocusInfo is the per-event payload the host hands to a
// ProcessAutoTagHandler. ProcessName is the lower-cased executable
// basename ("teams.exe"); WindowTitle is whatever the OS reports. The
// host pre-filters by the plugin's declared ProcessNames, so the
// handler is only ever asked about a process it has opted into.
type ProcessFocusInfo struct {
	// ProcessName is the lower-cased executable basename.
	ProcessName string
	// WindowTitle is the verbatim window title at the time of the event.
	// May be empty.
	WindowTitle string
	// IsCommunication is true when this event came from the comm-track
	// rail (Teams, Zoom, …) rather than the focused-window rail.
	IsCommunication bool
}

// ProcessAutoTagResult is the plugin's response to Resolve. Match=false
// means "skip this event" (the host falls back to no-tag, same as if
// the plugin had not been consulted). Match=true with an empty TagName
// is rejected — the host treats that as an opt-out.
type ProcessAutoTagResult struct {
	// Match must be true for the host to act on this result. Set false
	// to opt out for a particular (process, title) pair without removing
	// the process from ProcessNames.
	Match bool
	// TagName is a slash-separated tag-hierarchy path, e.g. "coding" or
	// "productivity/coding". The host resolves the path against the
	// existing tags table, creating any missing intermediate nodes
	// (matching by name, case-insensitive). An empty TagName is treated
	// as Match=false.
	TagName string
	// Description is optional free-form text attached to the resulting
	// tag-block. Empty ⇒ no description.
	Description string
}

// ProcessAutoTagHandler is implemented by plugins advertising
// CapProcessAutoTag. The host calls ProcessNames() once at Configure
// time to learn which executable basenames the plugin wants to be
// consulted about, then calls Resolve only for events whose process
// matches.
//
// The handler acts as a fallback behind the user's hand-maintained
// rules: when an enabled rule matches the focused window, the rule
// wins and the plugin is not consulted. This keeps plugins from
// surprising the user.
//
// Resolve runs on the orchestrator's hot path (every focus change to a
// claimed process). Plugins SHOULD return quickly — the host applies
// a per-call timeout and drops the result on expiry.
type ProcessAutoTagHandler interface {
	// ProcessNames lists the executable basenames (case-insensitive)
	// the plugin wants to be consulted about. Called once after every
	// Configure(). Returning nil or an empty slice puts the handler in
	// a dormant state — it will not be consulted on any event.
	ProcessNames(ctx context.Context) ([]string, error)

	// Resolve is invoked when the focused (or comm-track) window's
	// process matches one of ProcessNames. The handler returns the tag
	// (and optional description) to use for the auto-tag-block, or
	// Match=false to opt out for this particular (process, title) pair.
	Resolve(ctx context.Context, info ProcessFocusInfo) (ProcessAutoTagResult, error)
}

// Sentinel errors plugins may return. Wrap them with fmt.Errorf("%w: detail", ...)
// to attach context.
var (
	// ErrConfigInvalid signals user-fixable misconfiguration. The host
	// surfaces the wrapping detail in the settings UI banner.
	ErrConfigInvalid = errors.New("plugin: config invalid")

	// ErrNotConfigured signals the plugin cannot serve its capability
	// because required settings are missing. Submit calls fail fast.
	ErrNotConfigured = errors.New("plugin: not configured")

	// ErrTransient signals the host that retry has a chance of succeeding
	// (network blip, remote 5xx, …). The doc stays in 'failed' status and
	// the user may click Retry.
	ErrTransient = errors.New("plugin: transient failure")

	// ErrUnknownSecretHandle is returned by HostAPI.RedeemSecret when the
	// handle is stale (host restart) or never issued.
	ErrUnknownSecretHandle = errors.New("plugin: unknown secret handle")
)

// Serve is the entry point a plugin's main() calls. It blocks until the
// host disconnects, then returns. Equivalent to:
//
//	hplugin.Serve(&hplugin.ServeConfig{
//	    HandshakeConfig: Handshake,
//	    Plugins:         PluginMap(impl),
//	})
func Serve(impl Plugin) {
	hplugin.Serve(&hplugin.ServeConfig{
		HandshakeConfig: Handshake,
		Plugins:         PluginMap(impl),
	})
}

// PluginMap returns the plugin set the SERVER side (plugin process)
// registers: always includes the core "plugin" service, plus a
// capability-specific service for every interface impl satisfies.
//
// The host calls HostSidePluginMap() to get the matching CLIENT-side set.
func PluginMap(impl Plugin) hplugin.PluginSet {
	set := hplugin.PluginSet{
		pluginKeyCore: &corePluginAdapter{impl: impl},
	}
	if h, ok := impl.(OnCallDocumentationHandler); ok {
		set[pluginKeyOnCall] = &oncallPluginAdapter{impl: h}
	}
	if h, ok := impl.(PluginManagementHandler); ok {
		set[pluginKeyMgmt] = &mgmtPluginAdapter{impl: h}
	}
	if h, ok := impl.(ProcessAutoTagHandler); ok {
		set[pluginKeyProcessAutoTag] = &processAutoTagPluginAdapter{impl: h}
	}
	return set
}

// HostSidePluginMap returns the plugin set the HOST registers with
// plugin.NewClient. Server-impl fields are nil — the host only dispenses
// clients.
func HostSidePluginMap() hplugin.PluginSet {
	return hplugin.PluginSet{
		pluginKeyCore:           &corePluginAdapter{},
		pluginKeyOnCall:         &oncallPluginAdapter{},
		pluginKeyMgmt:           &mgmtPluginAdapter{},
		pluginKeyProcessAutoTag: &processAutoTagPluginAdapter{},
	}
}

// CoreKey, OnCallKey, MgmtKey, and ProcessAutoTagKey export the
// plugin-set keys so the host can Dispense() the right service.
const (
	CoreKey           = pluginKeyCore
	OnCallKey         = pluginKeyOnCall
	MgmtKey           = pluginKeyMgmt
	ProcessAutoTagKey = pluginKeyProcessAutoTag
)

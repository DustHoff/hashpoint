# Plugin API reference

Every plugin import is `github.com/dusthoff/hashpoint/internal/plugin/sdk`.
For v1 the SDK is internal — plugin authors must build their binary
inside the Hashpoint repository (typically under `cmd/<plugin-name>/`).
Promoting the SDK to a public Go module is a planned mechanical change
that keeps the interfaces below unchanged.

## Quick start

```go
package main

import (
    "context"

    sdk "github.com/dusthoff/hashpoint/internal/plugin/sdk"
)

type myPlugin struct {
    host        sdk.HostAPI
    endpoint    string
    tokenHandle sdk.SecretHandle
}

func (p *myPlugin) Init(_ context.Context, host sdk.HostAPI) error {
    p.host = host
    return nil
}

func (p *myPlugin) Metadata(_ context.Context) (sdk.Metadata, error) {
    return sdk.Metadata{
        Name:         "oncall-example",
        Version:      "0.1.0",
        APIVersion:   sdk.HostAPIVersion,
        Capabilities: []sdk.Capability{sdk.CapOnCallDocumentation},
        Description:  "Pushes on-call docs to <somewhere>",
    }, nil
}

func (p *myPlugin) Configure(_ context.Context, cfg sdk.PluginConfig) error {
    p.endpoint = cfg.Fields["endpoint"]
    p.tokenHandle = cfg.Secrets["api_token"]
    if p.endpoint == "" || p.tokenHandle == "" {
        return sdk.ErrNotConfigured
    }
    return nil
}

func (p *myPlugin) Submit(ctx context.Context, doc sdk.OnCallDocument) (sdk.SubmissionResult, error) {
    token, err := p.host.RedeemSecret(ctx, p.tokenHandle)
    if err != nil {
        return sdk.SubmissionResult{}, err
    }
    _ = token
    // ... do an HTTP POST etc. ...
    return sdk.SubmissionResult{
        ExternalRef: "EX-42",
        ExternalURL: "https://example.com/EX-42",
    }, nil
}

func main() { sdk.Serve(&myPlugin{}) }
```

## Core interface

Every plugin implements `sdk.Plugin`. Lifecycle is strictly
`Init → Metadata → Configure`, then capability methods on demand.

```go
type Plugin interface {
    Init(ctx context.Context, host HostAPI) error
    Metadata(ctx context.Context) (Metadata, error)
    Configure(ctx context.Context, cfg PluginConfig) error
}
```

- `Init` is called exactly once. The `HostAPI` stays valid for the
  plugin's lifetime; keep it on the receiver.
- `Metadata` must be cheap and side-effect free.
  `Metadata.APIVersion` MUST equal `sdk.HostAPIVersion`.
- `Configure` is called once after `Metadata`, then again on every
  settings save. Return `sdk.ErrConfigInvalid` (wrapped via
  `fmt.Errorf("%w: detail", ...)`) for user-fixable misconfiguration —
  the host surfaces the wrapped detail in the settings UI.

## Metadata

```go
type Metadata struct {
    Name         string         // must equal the plugin's directory name
    Version      string         // semver, shown in the settings UI
    APIVersion   int            // MUST equal sdk.HostAPIVersion
    Capabilities []Capability   // see below
    Description  string         // one-line, shown in the settings UI
}
```

## Capabilities

```go
type Capability string

const (
    CapOnCallDocumentation Capability = "oncall_documentation"
    CapPluginManagement    Capability = "plugin_management"
)
```

Implementing a capability means implementing the matching Go interface.
The SDK's `Serve` function inspects the impl's type at startup and only
exposes capabilities the impl actually satisfies — there is no separate
registration step.

### `oncall_documentation`

```go
type OnCallDocumentationHandler interface {
    Submit(ctx context.Context, doc OnCallDocument) (SubmissionResult, error)
}

type IncidentType string
const (
    IncidentPlannedMaintenance IncidentType = "planned_maintenance"
    IncidentServiceDisruption  IncidentType = "service_disruption"
)

type OnCallDocument struct {
    LocalID      string       // stable per Hashpoint doc; use as a remote dedup key
    BlockID      int64        // Hashpoint primary key, not stable across DB resets
    StartTime    time.Time    // UTC
    EndTime      time.Time    // UTC
    TagName      string       // resolved display name (e.g. "#oncall/billing")
    Application  string       // user-entered, the impacted system
    IncidentType IncidentType
    Solution     string       // user-entered, free text
}

type SubmissionResult struct {
    ExternalRef string // shown as a chip in the inbox
    ExternalURL string // optional clickable link
}
```

`Submit` MUST be idempotent — use `OnCallDocument.LocalID` as a
deduplication key when filing tickets remotely so a retried `Submit`
does not create duplicates.

Return `sdk.ErrTransient` (wrap with `fmt.Errorf("%w: …", sdk.ErrTransient, …)`)
when the failure looks retryable (HTTP 5xx, network blip). The host
keeps the per-plugin submission in `failed` state; the user can click
Retry. Non-transient errors are surfaced verbatim in the inbox.

### `plugin_management`

```go
type AvailablePlugin struct {
    Name        string  // becomes the plugin's directory + manifest name
    Version     string  // semver, shown in the catalog
    Description string  // one-line, shown in the catalog
}

type PluginManagementHandler interface {
    ListAvailable(ctx context.Context) ([]AvailablePlugin, error)
    Install(ctx context.Context, name string) error
    Update(ctx context.Context, name string) error
    Uninstall(ctx context.Context, name string) error
}
```

A plugin advertising `plugin_management` is a *plugin source*. Its
catalog is merged with every other source's catalog in the
**Verfügbare Plugins** tab — each row carries the source plugin's name
so Install / Update / Uninstall route back to the originating handler.

Contract with the host:

- `Install` and `Update` create or replace files under
  `<PluginsDir>/<name>/` (binary + `manifest.toml`). The host launches
  the freshly-written plugin after a successful `Install`.
- The host stops the target plugin's subprocess **before** calling
  `Update` — on Windows the running `.exe` is file-locked and the
  handler could not overwrite it otherwise.
- `Uninstall` removes `<PluginsDir>/<name>/` from disk. The host
  clears the target's `plugin_state` + `plugin_settings` rows after
  `Uninstall` returns; the handler must not touch the database.
- A source cannot uninstall itself — the host returns
  `ErrSelfUninstallRefused` and never invokes the handler.
- Errors are surfaced verbatim in the UI; wrap with context.

## HostAPI

The reverse-RPC surface plugins use to talk back to the host:

```go
type HostAPI interface {
    RedeemSecret(ctx context.Context, h SecretHandle) (string, error)
    Log(ctx context.Context, level, message string, fields map[string]string) error
}
```

### `RedeemSecret`

```go
type SecretHandle string
```

Opaque token the host delivers via `PluginConfig.Secrets`. The plugin
calls `RedeemSecret` on-demand to obtain the plaintext, then discards
it. Handles are minted fresh on every `Configure` and revoked on
plugin reload — a leaked handle dies on next host restart.

Return values:

- `ErrUnknownSecretHandle` if the handle is stale (host restart or
  config change since it was issued) or was minted for a different
  plugin. Treat as non-retryable — the user must re-save the secret
  in the settings UI.

### `Log`

Forwards a structured log line to the host's `slog` handler with the
plugin's name prepended. Levels: `debug`, `info`, `warn`, `error`
(unknown levels degrade to `info`). The host strips any `plugin` field
the caller tries to set, to keep the attribution truthful.

## Configuration model

```go
type PluginConfig struct {
    Fields  map[string]string         // values for type=text and type=boolean fields
    Secrets map[string]SecretHandle   // handles for type=password fields
}

type FieldType string
const (
    FieldTypeText     FieldType = "text"
    FieldTypePassword FieldType = "password"
    FieldTypeBool     FieldType = "boolean"
)
```

Every config field is declared in `manifest.toml` under
`[config_schema.fields.<key>]` with a `type`, `label`, `required`
flag, and optional `default`. The host derives the persistence and
delivery strategy from `type`:

| Type       | UI input                  | Storage          | Delivered to plugin via    |
|------------|---------------------------|------------------|----------------------------|
| `text`     | single-line text input    | `plugin_settings` (plain) | `PluginConfig.Fields[key]` |
| `boolean`  | toggle (`"true"`/`"false"`) | `plugin_settings` (plain) | `PluginConfig.Fields[key]` (string) |
| `password` | masked input              | `plugin_settings` (DPAPI-encrypted) | `PluginConfig.Secrets[key]` (handle) |

Boolean values cross the wire as the literal strings `"true"` /
`"false"` — parse with `strconv.ParseBool` plugin-side.

The host applies the manifest `default` for any plain field the user
has not filled in. Required fields with no stored value and no default
park the plugin in `state=needs_config`; `Configure` is never called
in this state — the subprocess does not run at all.

Secret values are NEVER sent over RPC. Each entry in `Secrets` is a
SecretHandle the plugin redeems via `HostAPI.RedeemSecret`.

## Manifest

`<plugin-dir>/manifest.toml` is the offline self-description the
settings UI renders even before the subprocess runs:

```toml
name = "oncall-jira"
version = "0.1.0"
api_version = 1
description = "Files Jira tickets for off-duty work"
capabilities = ["oncall_documentation"]

[config_schema.fields.endpoint]
label = "Jira base URL"
type = "text"
required = true

[config_schema.fields.dry_run]
label = "Dry run"
type = "boolean"
required = false
default = "false"

[config_schema.fields.api_token]
label = "API token"
type = "password"
required = true
```

Rules:

- `name` MUST equal the plugin's directory name.
- `api_version` MUST equal `sdk.HostAPIVersion`.
- Every field `type` MUST be one of `text`, `password`, `boolean` —
  unknown types are rejected at load.
- There is no separate `secrets` section anymore; password-typed
  fields ARE the secret declarations.

## Sentinel errors

```go
var (
    ErrConfigInvalid       = errors.New("plugin: config invalid")
    ErrNotConfigured       = errors.New("plugin: not configured")
    ErrTransient           = errors.New("plugin: transient failure")
    ErrUnknownSecretHandle = errors.New("plugin: unknown secret handle")
)
```

Always wrap with detail: `fmt.Errorf("%w: missing endpoint", sdk.ErrConfigInvalid)`.
The host calls `errors.Is(err, sdk.ErrConfigInvalid)` etc. across the
wire — the rpc layer reconstructs the sentinel kind on the host side
based on flags in the reply struct (it doesn't rely on the raw error
message).

## Entry point: `sdk.Serve`

```go
func Serve(impl Plugin)
```

Blocks until the host disconnects. Equivalent to:

```go
hplugin.Serve(&hplugin.ServeConfig{
    HandshakeConfig: sdk.Handshake,
    Plugins:         sdk.PluginMap(impl),
})
```

`PluginMap(impl)` includes the `plugin` key for the core interface and
adds a capability-specific key for every interface `impl` satisfies.
The host's matching `HostSidePluginMap()` is symmetric — both sides
must agree on the keys for the handshake to succeed.

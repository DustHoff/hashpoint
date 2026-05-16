# Plugin API reference

Every plugin import is `github.com/dusthoff/hashpoint/plugin/sdk`. The
SDK lives under `plugin/` (outside `internal/`), so plugin authors can
build their binary in a separate module and `go get` the SDK package
directly.

## Quick start

```go
package main

import (
    "context"

    sdk "github.com/dusthoff/hashpoint/plugin/sdk"
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
    CapOffHoursProvider    Capability = "off_hours_provider"
    CapPluginManagement    Capability = "plugin_management"
    CapProcessAutoTag      Capability = "process_autotag"
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

### `off_hours_provider`

```go
type OffHoursKind string
const (
    OffHoursAdd    OffHoursKind = "add"    // mark range as off-hours
    OffHoursRemove OffHoursKind = "remove" // mark range as working-hours
)

type OffHoursRequest struct {
    From time.Time // UTC, half-open lower bound
    To   time.Time // UTC, half-open upper bound
}

type OffHoursInterval struct {
    Start, End time.Time
    Kind       OffHoursKind // empty string ≙ "add"
    Reason     string       // UI tooltip ("Christmas Day", "Planned shift")
    Source     string       // optional plugin-internal key (e.g. "DE-NW")
}

type OffHoursProviderHandler interface {
    OffHours(ctx context.Context, req OffHoursRequest) ([]OffHoursInterval, error)
}
```

The handler returns intervals that intersect `[req.From, req.To)`. The
host calls it pull-based during on-call qualification (after a block is
closed / re-tagged / resized) and caches results per plugin in a
year-bucket in memory. Cache entries are dropped when the plugin
reloads, stops, or crashes — there is no persistence.

Effective off-hours timeline = `WorkScheduleConfig` ∪ (all plugin
`add`s) − (all plugin `remove`s). **`remove` wins globally**: a
`remove` interval can carve a working-hour window out of a non-working
weekday and out of another plugin's `add`.

See [capability-off-hours-provider.md](./capability-off-hours-provider.md)
for the full semantics, including the "no backfill" rule that applies
when a provider plugin is freshly installed.

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

### `process_autotag`

```go
type ProcessFocusInfo struct {
    ProcessName     string // lower-cased basename, e.g. "code.exe"
    WindowTitle     string // verbatim; may be empty
    IsCommunication bool   // true ⇒ event came from the comm-track rail
}

type ProcessAutoTagResult struct {
    Match       bool   // false ⇒ skip this event
    TagName     string // slash-separated tag path; created if missing
    Description string // optional, attached to the resulting block
}

type ProcessAutoTagHandler interface {
    ProcessNames(ctx context.Context) ([]string, error)
    Resolve(ctx context.Context, info ProcessFocusInfo) (ProcessAutoTagResult, error)
}
```

`ProcessNames` declares the executable basenames the plugin wants to be
consulted about. It is called once after every `Configure()`; the host
caches the result.

`Resolve` is invoked only when the focused (or comm-track) window's
process matches `ProcessNames` AND no user rule already matches the
focused window — user rules always win. The plugin can still opt out
for a particular `(processName, windowTitle)` pair by returning
`Match: false`.

`TagName` is a slash-separated hierarchy path; the host materialises
it against the tags table (case-insensitive), creating any missing
intermediate nodes. See
[capability-process-autotag.md](./capability-process-autotag.md) for
normalisation rules, conflict semantics, and the comm-rail behaviour.

Resolve runs on the orchestrator's hot path. The host applies a tight
per-call timeout (default 500 ms); slow plugins are dropped for that
event without aborting other plugins.

### `tag_provider`

```go
type ImportedTag struct {
    Path        string // slash-separated, e.g. "jira/PROJ-123"
    Description string // optional; honoured only on first create
    Color       string // optional hex (e.g. "#7c3aed"); same rule
}

type Order struct {
    ID          string // opaque per-plugin de-dupe key
    Name        string // shown in the Auftrag combobox AND stored on the tag
    Description string // optional helper text in the dropdown
}

type TagOrderMapping struct {
    TagPath   string // slash-separated path, segments without leading '#'
    OrderName string // value stored on tags.order_name; "" when unmapped
}

type TagProviderHandler interface {
    ListTags(ctx context.Context) ([]ImportedTag, error)
    ListOrders(ctx context.Context) ([]Order, error)
    NotifyTagOrders(ctx context.Context, mappings []TagOrderMapping) error
}
```

The host pulls `ListTags` at plugin launch, on `Configure`, and on the
user's "Tags neu laden" click in the Plugins tab. A plugin may also
push imports at any time via `HostAPI.PublishTags`.

`ListOrders` powers the per-tag *Auftrag* combobox in the Tag-Manager
and is queried **live** every time the user opens the Tags tab — the
host never caches the result. A plugin without an order catalogue
should return `(nil, nil)`.

`NotifyTagOrders` is push-from-host (fire-and-forget) and fires on
every user-initiated `CreateTag` / `UpdateTag` / `DeleteTag`. The
payload is a full snapshot of every tag with its current `OrderName`
(empty when unmapped), letting a plugin diff against its last
snapshot and react to changes in its upstream system. Plugins without
interest should return `nil` immediately.

See [capability-tag-provider.md](./capability-tag-provider.md) for the
full merge contract, lifecycle, conflict examples, and order /
notification semantics.

## HostAPI

The reverse-RPC surface plugins use to talk back to the host:

```go
type HostAPI interface {
    RedeemSecret(ctx context.Context, h SecretHandle) (string, error)
    Log(ctx context.Context, level, message string, fields map[string]string) error
    RequestEntraToken(ctx context.Context, scopes []string) (token string, expiresAt time.Time, err error)
    RequestPersonioSession(ctx context.Context) (PersonioSession, error)
    ListTags(ctx context.Context) ([]HostTag, error)
    PublishTags(ctx context.Context, tags []ImportedTag) (created int, err error)
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

### `RequestEntraToken`

Returns a Bearer-suitable Microsoft Entra ID access token for the
given scopes plus its UTC expiry. Used by plugins that call Microsoft
Graph endpoints or Entra-protected custom APIs on the signed-in
user's behalf.

```go
token, expiresAt, err := host.RequestEntraToken(ctx, []string{
    "https://graph.microsoft.com/User.Read",
})
if errors.Is(err, sdk.ErrEntraNotAvailable) {
    // Entra is off, user signed out, or the scope needs consent.
    // Fall back to a no-Entra code path — do not retry tightly.
    return nil
}
if err != nil {
    return err
}
// Use `token` as the Authorization: Bearer header value, then discard.
_ = expiresAt // useful for skipping a re-request right before expiry.
```

The host serves the call **silently** via MSAL — refreshing the access
token from the persisted refresh token transparently when the cache
copy is stale. The refresh token itself never crosses the
host↔plugin boundary, by design: a compromised plugin can mint only
access tokens for the duration of the host process.

Return values:

- `(token, expiresAt, nil)` on success. The plaintext is in-memory
  only — never log or persist it. Plugins SHOULD discard the value
  once the outbound HTTP call completes and re-request on the next
  cadence rather than caching long-lived state.
- `ErrEntraNotAvailable` (wrapped) when Entra is not configured, no
  user is signed in, the refresh token expired, or the requested
  scopes need fresh interactive consent. The plugin MUST treat this
  as a recoverable, capability-specific limitation — not a fatal
  error — and either skip its feature or surface a `Log("warn", …)`
  hint.

Scope model: plugins request whatever scopes they need at runtime
(typically Graph URIs, e.g. `https://graph.microsoft.com/Mail.Read`).
The host does **not** enforce a per-plugin allowlist; it forwards the
scopes straight to MSAL. Consent is the user's normal Entra flow —
out-of-band of the plugin RPC.

The host never escalates to an interactive flow on a plugin's behalf:
mid-session browser pop-ups initiated by background plugins would be
hostile. If the silent path fails, the plugin gets
`ErrEntraNotAvailable` and the user must re-sign-in via Hashpoint's
own UI.

### `RequestPersonioSession`

Returns the host's current Personio session material so the plugin
can call the same internal UI API Hashpoint itself uses for the sync
feature (see `docs/hashpoint-spec.md` §2.5). The payload contains the
per-tenant app host, the URL-decoded CSRF token, and the raw session
cookies — everything the plugin needs to populate an `http.CookieJar`
and forge an authenticated request.

```go
type PersonioSession struct {
    AppHost    string           // e.g. "acme.app.personio.com"
    CSRFToken  string           // send as the "x-athena-xsrf-token" header
    Cookies    []PersonioCookie // replay via http.CookieJar
    CapturedAt time.Time        // informational; the host has already gated freshness
}

type PersonioCookie struct {
    Name, Value, Domain, Path string
    Expires                   time.Time
    Secure, HTTPOnly          bool
    SameSite                  string // "lax" | "strict" | "none" | ""
}
```

```go
sess, err := host.RequestPersonioSession(ctx)
if errors.Is(err, sdk.ErrPersonioNotAvailable) {
    // No tenant configured, source not wired, or the user dismissed
    // the re-authentication window. Skip the feature this round.
    return nil
}
if err != nil {
    return err
}

jar, _ := cookiejar.New(nil)
base, _ := url.Parse("https://" + sess.AppHost + "/")
httpCookies := make([]*http.Cookie, 0, len(sess.Cookies))
for _, c := range sess.Cookies {
    httpCookies = append(httpCookies, &http.Cookie{
        Name: c.Name, Value: c.Value, Domain: c.Domain, Path: c.Path,
        Secure: c.Secure, HttpOnly: c.HTTPOnly,
    })
}
jar.SetCookies(base, httpCookies)

client := &http.Client{Jar: jar, Timeout: 15 * time.Second}
req, _ := http.NewRequestWithContext(ctx, "GET",
    "https://"+sess.AppHost+"/api/v1/navigation/context", nil)
req.Header.Set("Accept", "application/json")
req.Header.Set("x-athena-xsrf-token", sess.CSRFToken)

resp, err := client.Do(req)
// … 401 / 403 ⇒ re-request via host.RequestPersonioSession(ctx)
```

The host serves the call by reading the stored session from the
Windows Credential Manager. When the session is missing or older than
`MaxSessionAge` (24 h) the host transparently drives an **interactive
Chrome login** via Chrome DevTools Protocol — exactly the same flow
the settings UI's "Bei Personio anmelden" button uses. While that
window is open the call blocks (up to ~5 minutes); concurrent
`RequestPersonioSession` calls from other plugins are serialised by
an internal mutex so only one Chrome window opens at a time.

Return values:

- `(PersonioSession, nil)` on success. The session is usable now: the
  host has either validated the freshness of the stored blob or just
  captured + validated a brand-new one.
- `ErrPersonioNotAvailable` (wrapped) when no tenant is configured,
  the host was not constructed with a session store, the user
  aborted/closed the Chrome login window, or the renewed session
  failed `/api/v1/navigation/context` validation. The plugin MUST
  treat this as a "feature off" state — not a fatal error.

Security notes:

- The cookies in the payload are the **full session secret**. A
  plugin that obtains them can perform any action the signed-in user
  can on Personio. Never log them, never persist them, never
  forward them off the host.
- Plugins SHOULD **re-request on 401/403** from Personio rather than
  caching the `PersonioSession` value across long idle periods. The
  user may sign out (or the server may invalidate the session) at any
  time, and a fresh `RequestPersonioSession` is the only way to
  recover.
- The host does **not** restrict which Personio endpoints a plugin
  may hit. Stay within the same internal UI API surface Hashpoint
  documents in `docs/hashpoint-spec.md` §2.5.2; calls that look
  unlike a normal user session risk getting the cookie set flagged
  on the Personio side.

### `ListTags`

```go
type HostTag struct {
    ID       int64
    Name     string
    ParentID int64 // 0 when the tag is a root
    Color    string
}

list, err := host.ListTags(ctx)
```

Returns the host's current tag set, flat with parent IDs (build the
tree caller-side). Available to **every plugin** regardless of
capability — useful e.g. for a `process_autotag` plugin that wants to
check whether a target tag already exists before suggesting an
`EnsureByPath` that would create it.

The projection deliberately omits Personio identifiers and
`sync_to_personio` — plugins do not need those, and exposing them
would widen the data surface beyond what the capability promises.

### `PublishTags`

```go
created, err := host.PublishTags(ctx, []sdk.ImportedTag{
    {Path: "jira/PROJ-123", Description: "Customer onboarding"},
})
```

Pushes tags into the host store. Restricted to plugins that
advertise `CapTagProvider` — others see `ErrPublishTagsNotAllowed`.

Each path goes through the same merge contract as the host-driven
pull at plugin launch:

- **Existing paths are no-ops.** A tag the user created, or that any
  prior import created, is never modified. The return value
  `created` reflects only newly-created leaves.
- `Description` and `Color` are honoured only when the leaf is being
  created in this call.
- Idempotent: calling with the same list twice in a row returns 0
  the second time.

See [capability-tag-provider.md](./capability-tag-provider.md) for
worked merge examples and the lifecycle (which is: there isn't one —
tags the plugin no longer reports stay in place).

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
    ErrConfigInvalid        = errors.New("plugin: config invalid")
    ErrNotConfigured        = errors.New("plugin: not configured")
    ErrTransient            = errors.New("plugin: transient failure")
    ErrUnknownSecretHandle  = errors.New("plugin: unknown secret handle")
    ErrEntraNotAvailable    = errors.New("plugin: entra token not available")
    ErrPersonioNotAvailable = errors.New("plugin: personio session not available")
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

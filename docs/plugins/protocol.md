# Wire protocol

Hashpoint plugins talk to the host over [hashicorp/go-plugin][hcl] in
**net/rpc mode**, multiplexed via yamux. Net/rpc was chosen over gRPC
to keep the host build pure-Go without a `protoc` toolchain; the SDK
contract (the Go interfaces in `plugin/sdk`) is transport-
agnostic, so a future migration to gRPC requires only swapping the
`rpc_*.go` wiring.

## Handshake

The handshake is a magic-cookie exchange (`hashicorp/go-plugin`
convention). Plugin and host MUST agree on:

```go
sdk.Handshake = hplugin.HandshakeConfig{
    ProtocolVersion:  1,
    MagicCookieKey:   "HASHPOINT_PLUGIN",
    MagicCookieValue: "v1-oncall-doc",
}
```

Mismatch ⇒ the plugin process exits immediately and the host records
`StateFailed` with a clear error. Change `MagicCookieValue` to
deliberately force every installed plugin to be rebuilt (e.g. after a
breaking SDK change unrelated to `ProtocolVersion`).

`sdk.HostAPIVersion` is a separate integer for SDK surface changes
(interface methods, types). The host checks both the manifest's
`api_version` (offline) and `Metadata.APIVersion` (after launch); both
MUST equal `HostAPIVersion`.

## Plugin set

Each plugin advertises a static set of named services. Both sides
register the same keys:

| Key                     | Service                                                      |
|-------------------------|--------------------------------------------------------------|
| `plugin`                | Core lifecycle: `Init`, `Metadata`, `Configure`              |
| `oncall_documentation`  | `Submit(OnCallDocument) → SubmissionResult`                  |
| `plugin_management`     | `ListAvailable`, `Install(name)`, `Update(name)`, `Uninstall(name)` |

Within each key, `net/rpc` exposes the methods under the prefix `Plugin.`
(`Plugin.Init`, `Plugin.Submit`, …). Different keys get separate
multiplexed streams, so a slow `Submit` cannot block `Configure`.

Adding a new capability is three edits in `plugin/sdk`:

1. add the `Capability` constant + the matching Go interface,
2. add a server/client pair in `rpc.go` mirroring `oncallServer` /
   `oncallClient`,
3. extend `PluginMap` / `HostSidePluginMap` with the new key.

## Reverse RPC: HostAPI

The plugin needs to call back into the host for three reasons: secret
redemption, structured logging, and Microsoft Entra ID access-token
acquisition. All three go through `HostAPI`.

The mechanism is `hashicorp/go-plugin`'s `MuxBroker`:

1. When the host calls `Plugin.Init`, the host-side `coreClient`
   allocates a fresh broker stream-ID, starts a `net/rpc` server on it
   serving `hostAPIServer`, and ships the stream-ID in `CoreInitArgs`.
2. The plugin-side `coreServer.Init` dials the stream, wraps the
   resulting `*rpc.Client` as a `hostAPIClient`, and hands it to the
   plugin author's `Init(ctx, host HostAPI)`.
3. `hostAPIClient` implements `sdk.HostAPI` by translating each method
   into a `client.Call("HostAPI.X", …)` against the dialled stream.

The host registers `HostAPI` server under the literal name `HostAPI` so
plugin-side calls use `client.Call("HostAPI.RedeemSecret", …)`.

## Error encoding

`net/rpc` flattens returned errors to a string, losing `errors.Is`
identity. To preserve sentinel error types across the wire, each
reply struct carries explicit discriminator flags:

```go
type CoreConfigureReply struct {
    Err      string
    IsConfig bool   // ⇒ rehydrate ErrConfigInvalid on the host
}

type OnCallSubmitReply struct {
    Result          SubmissionResult
    Err             string
    IsTransient     bool   // ⇒ rehydrate ErrTransient
    IsNotConfigured bool   // ⇒ rehydrate ErrNotConfigured
}

type HostRedeemSecretReply struct {
    Value     string
    Err       string
    IsUnknown bool   // ⇒ rehydrate ErrUnknownSecretHandle
}

type HostRequestEntraTokenReply struct {
    Token          string
    ExpiresAt      time.Time
    Err            string
    IsNotAvailable bool  // ⇒ rehydrate ErrEntraNotAvailable
}
```

The client side reconstructs the wrapped sentinel:

```go
if reply.IsTransient {
    return res, fmt.Errorf("%w: %s", ErrTransient, reply.Err)
}
```

The plugin authors return the wrapped sentinel as usual; the encoding
is transparent.

## Secret model

Per-plugin configuration — including secrets — is persisted in the
SQLite database in the `plugin_settings` table (one row per
`(plugin_name, key)`). Secret rows have `is_secret = 1` and their
`value` blob is DPAPI-encrypted (CurrentUser scope) before insertion,
so a stolen `data.db` is unreadable from any other Windows user
account. Plain `text` / `boolean` rows are stored as UTF-8 bytes.

For every password-typed field declared in the manifest the host:

1. Mints a fresh random 128-bit hex `SecretHandle`.
2. Stores the mapping `handle → (plugin-name, secret-key)` in an
   in-memory registry.
3. Sends the handle to the plugin in `PluginConfig.Secrets`. The
   cleartext does NOT cross the wire at `Configure()` time.

When the plugin calls `HostAPI.RedeemSecret(handle)`:

1. The host looks up the registry entry. Stale handles return
   `ErrUnknownSecretHandle`.
2. The host verifies the caller's plugin-name matches the entry's
   plugin-name (defence against a leaked handle being replayed by a
   different plugin).
3. The host runs the encrypted blob through the DPAPI cipher and
   returns the plaintext. The plaintext lives in memory only for the
   duration of the plugin's outbound call.

Handles are NOT persisted. A host restart, a plugin reload, or a
config change re-mints every handle — leaked handles die quickly.

## Enable/disable flag

The `plugin_state` table holds one row per plugin with an
`enabled` boolean (default `1`). On startup the host loads the row
for every directory under `PluginsDir`; disabled plugins are recorded
as `state=disabled` and never launched. Toggling the flag in the
settings UI calls `Host.SetEnabled` which persists the new value and
either tears down the subprocess (enabled → disabled) or fires a fresh
launch (disabled → enabled).

## Required-field gate (`state=needs_config`)

After loading the manifest the host reads the persisted values for
every field. If any field with `required = true` is missing a value
(no row in `plugin_settings` AND no `default` in the manifest), the
plugin is parked in `state=needs_config` and the missing keys are
attached to its `PluginInfo.MissingFields`. The subprocess is **not**
started in this state and capability fan-outs skip the plugin. Saving
the missing values from the Plugins tab calls `SetConfig`/`SetSecret`
followed by a reload, which re-runs the required-field gate.

## Per-plugin timeout

`Host.SubmitOnCallDoc` enforces a per-call timeout (default 30s,
overridable via `HostDeps.SubmitTimeout`). A plugin that exceeds the
deadline gets a context cancellation; its submission row stays in
`pending` until the next retry.

## Periodic discovery

After the initial scan, the host re-reads `PluginsDir` every
`HostDeps.DiscoveryInterval` (default 30 s, negative ⇒ disabled).
Subdirectories absent from the in-memory registry are passed through
the regular `launch()` path — manifest load, required-field gate,
handshake — so a plugin dropped into the folder while the app is
running starts on its own without an app restart. The default
`plugin_state.enabled = 1` row means freshly-discovered plugins boot
straight into `StateRunning` (or `needs_config`) without an explicit
opt-in.

For each plugin the discovery loop picks up, the host invokes
`HostDeps.OnDiscovered(Info)`. The App layer forwards this to the
Wails event `plugins:discovered` so both the **Plugins** and the
**Verfügbare Plugins** tabs refresh live.

Plugins already known to the host — including ones in `failed`,
`disabled`, or `needs_config` — are left untouched on each tick.
Manually-deleted plugin directories are intentionally **not** cleaned
up; their entries stay in the list until the next app restart.

## Install / Update / Uninstall flow

`Host.InstallPlugin(source, name)`, `Host.UpdatePlugin(source, name)`,
and `Host.UninstallPlugin(source, name)` dispatch to whichever running
plugin advertises `plugin_management` under `source`. The host wraps
each call so the handler never has to think about subprocess lifecycle:

- **Install** — the host calls `handler.Install(name)`, then launches
  the freshly-written plugin via the regular `launch()` path. The
  install is rejected if `name` is already known to the host (use
  Update instead).
- **Update** — the host stops the target subprocess via
  `stopAndForget(name)` (kill client, revoke `SecretHandle`s, drop
  the in-memory entry), then calls `handler.Update(name)`, then
  relaunches. If `handler.Update` fails the host attempts a relaunch
  anyway so the target's state is still visible in the UI.
- **Uninstall** — the host refuses self-uninstall
  (`ErrSelfUninstallRefused`), stops the target, calls
  `handler.Uninstall(name)`, then calls `SettingsStore.Clear(name)`
  which deletes the `plugin_state` row and every `plugin_settings`
  row for the plugin in a single transaction. The plugin is fully
  gone from the host's view — a future Install starts from manifest
  defaults.

## Crash isolation

`hashicorp/go-plugin` watches the plugin subprocess. A plugin panic
prints a stack trace to stderr (forwarded to the host's `slog` via
the hclog adapter) and exits non-zero; the host marks the plugin
`failed` and continues serving the rest of Hashpoint. Reload restarts
the subprocess.

[hcl]: https://github.com/hashicorp/go-plugin

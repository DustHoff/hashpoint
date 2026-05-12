# Wire protocol

Hashpoint plugins talk to the host over [hashicorp/go-plugin][hcl] in
**net/rpc mode**, multiplexed via yamux. Net/rpc was chosen over gRPC
to keep the host build pure-Go without a `protoc` toolchain; the SDK
contract (the Go interfaces in `internal/plugin/sdk`) is transport-
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

| Key                     | Service                                         |
|-------------------------|-------------------------------------------------|
| `plugin`                | Core lifecycle: `Init`, `Metadata`, `Configure` |
| `oncall_documentation`  | `Submit(OnCallDocument) → SubmissionResult`     |

Within each key, `net/rpc` exposes the methods under the prefix `Plugin.`
(`Plugin.Init`, `Plugin.Submit`, …). Different keys get separate
multiplexed streams, so a slow `Submit` cannot block `Configure`.

Adding a new capability is three edits in `internal/plugin/sdk`:

1. add the `Capability` constant + the matching Go interface,
2. add a server/client pair in `rpc.go` mirroring `oncallServer` /
   `oncallClient`,
3. extend `PluginMap` / `HostSidePluginMap` with the new key.

## Reverse RPC: HostAPI

The plugin needs to call back into the host for two reasons:
secret redemption and structured logging. Both go through `HostAPI`.

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

The host stores secrets in Windows Credential Manager under
`TimeTracker:plugin:<plugin-name>:<key>` (User scope, DPAPI-protected
by the OS). At every `Configure` call, the host:

1. Mints a fresh random 128-bit hex `SecretHandle` for each secret key
   declared in the plugin's manifest.
2. Stores the mapping `handle → (plugin-name, secret-key)` in an
   in-memory registry.
3. Sends the handles to the plugin in `PluginConfig.Secrets`.

When the plugin calls `HostAPI.RedeemSecret(handle)`:

1. The host looks up the registry entry. Stale handles return
   `ErrUnknownSecretHandle`.
2. The host verifies the caller's plugin-name matches the entry's
   plugin-name (defence against a leaked handle being replayed by a
   different plugin).
3. The host fetches plaintext from the credential store and returns
   it. The plaintext lives in memory only for the duration of the
   plugin's outbound call.

Handles are NOT persisted. A host restart, a plugin reload, or a
config change re-mints every handle — leaked handles die quickly.

## Per-plugin timeout

`Host.SubmitOnCallDoc` enforces a per-call timeout (default 30s,
overridable via `HostDeps.SubmitTimeout`). A plugin that exceeds the
deadline gets a context cancellation; its submission row stays in
`pending` until the next retry.

## Crash isolation

`hashicorp/go-plugin` watches the plugin subprocess. A plugin panic
prints a stack trace to stderr (forwarded to the host's `slog` via
the hclog adapter) and exits non-zero; the host marks the plugin
`failed` and continues serving the rest of Hashpoint. Reload restarts
the subprocess.

[hcl]: https://github.com/hashicorp/go-plugin

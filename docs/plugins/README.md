# Hashpoint Plugin System

Hashpoint plugins are separate executables that the host (Hashpoint itself)
launches as subprocesses. Communication is over `net/rpc`, multiplexed via
[hashicorp/go-plugin][hcl], so a plugin crash never crashes the host.

Today the system supports one capability: **on-call documentation
("Rufbereitschaft")**. A plugin advertising `oncall_documentation` receives
filled-out off-duty docs from the user (solution / impacted application /
incident type) and pushes them into whatever downstream system you choose
(Jira, OTRS, Confluence, an internal webhook — none of that is shipped by
default).

Read on for:

- [Where plugins live on disk](#installation-layout)
- [The plugin contract (Go API)](api.md)
- [Wire protocol, handshake, secret model](protocol.md)
- [On-call documentation capability spec](capability-oncall-documentation.md)

---

## Installation layout

```
%APPDATA%\TimeTracker\plugins\
  oncall-jira\
    oncall-jira.exe       (or `oncall-jira` on non-Windows builds)
    manifest.toml
  oncall-otrs\
    oncall-otrs.exe
    manifest.toml
```

The directory name **must** match the plugin's `name` in `manifest.toml`.
Hashpoint refuses to load a plugin where they disagree (catches "I renamed
the folder but not the manifest").

A minimal manifest looks like:

```toml
name        = "oncall-example"
version     = "0.1.0"
api_version = 1
description = "Pushes on-call docs to <somewhere>"
capabilities = ["oncall_documentation"]

[config_schema.fields.endpoint]
label    = "Endpoint URL"
type     = "string"
required = true

[config_schema.secrets.api_token]
label    = "API Token"
required = true
```

The host renders the settings UI from `config_schema` alone — it doesn't
need to launch the plugin to know what fields the plugin wants.

`api_version` MUST equal Hashpoint's current `sdk.HostAPIVersion`. A
mismatch surfaces in the settings UI with a clear error message; the
plugin stays in `failed` state until rebuilt against the matching SDK.

## Configuration

Field values go into `%APPDATA%\TimeTracker\config.toml`:

```toml
[plugins.oncall-example]
endpoint = "https://jira.example.com/rest/servicedeskapi"
```

Secrets are stored in Windows Credential Manager under target
`TimeTracker:plugin:<plugin-name>:<key>`, written via the in-app settings
UI. **Secrets never appear in config.toml** and never cross the
host↔plugin boundary at config time — the plugin receives an opaque
`SecretHandle` and redeems it via `HostAPI.RedeemSecret` only at the
moment it needs the plaintext.

## Lifecycle

1. **Discovery** — at host startup, Hashpoint scans `PluginsDir` for
   subdirectories with a valid `manifest.toml`.
2. **Launch** — each plugin's executable is started; the host and plugin
   shake hands via the magic-cookie protocol (mismatch ⇒ launch fails).
3. **Init** — host calls `Plugin.Init(host HostAPI)`. The plugin stores
   the HostAPI reference for later reverse-RPC.
4. **Metadata** — host calls `Plugin.Metadata()` to learn name, version,
   capabilities. The host caches the result.
5. **Configure** — host calls `Plugin.Configure(cfg)` with the merged
   field values + fresh SecretHandles. Called again on every settings
   save.
6. **Use** — when the user submits an on-call doc, the host fans the
   payload out to every running `oncall_documentation` plugin in
   parallel. Per-plugin results are persisted; the inbox refreshes live.
7. **Shutdown** — `Host.Stop()` kills every subprocess; SecretHandles
   are dropped (a leaked handle dies on host restart).

## When to write a plugin

The plugin system is intentionally minimal — write a plugin when:

- you need to push Hashpoint data to a system Hashpoint doesn't natively
  support (Jira Service Management, OTRS/Znuny, ServiceNow, an internal
  webhook, …), or
- you want to integrate behaviours that are too organisation-specific
  to ever go into Hashpoint core.

Don't write a plugin for behaviours that touch only Hashpoint's own
data (timeline rendering, tag rules, sync logic, …) — those belong in
the main app.

## Future capabilities

`oncall_documentation` is the first capability. Adding new ones is a
small SDK change: a new interface, a new wire-protocol service, an entry
in the host's plugin set. See [`api.md`](api.md) for the pattern.

[hcl]: https://github.com/hashicorp/go-plugin

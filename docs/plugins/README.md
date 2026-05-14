# Hashpoint Plugin System

Hashpoint plugins are separate executables that the host (Hashpoint itself)
launches as subprocesses. Communication is over `net/rpc`, multiplexed via
[hashicorp/go-plugin][hcl], so a plugin crash never crashes the host.

Three capabilities are defined today:

- **`oncall_documentation`** — receives filled-out off-duty docs from the
  user (solution / impacted application / incident type) and pushes them
  into whatever downstream system you choose (Jira, OTRS, Confluence,
  an internal webhook — none of that is shipped by default).
- **`plugin_management`** — acts as a plugin source. The handler surfaces
  a catalog of plugins available for install, and on user action writes
  them into `PluginsDir` (or removes them). The host orchestrates the
  subprocess stop/start dance around mutating writes and the database
  cleanup on uninstall. The reference implementation for this capability
  is also shipped as a plugin — Hashpoint core only defines the contract.
- **`process_autotag`** — declares one or more executable basenames the
  plugin wants to be consulted about. When the user has not configured a
  matching rule for the focused (or comm-track) process, the host asks
  the plugin which tag to apply, then opens an auto-tag-block. User
  rules always win — the plugin sits behind them as a fallback.

Read on for:

- [Where plugins live on disk](#installation-layout)
- [The plugin contract (Go API)](api.md)
- [Wire protocol, handshake, secret model](protocol.md)
- [On-call documentation capability spec](capability-oncall-documentation.md)
- [Process auto-tag capability spec](capability-process-autotag.md)

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
type     = "text"
required = true

[config_schema.fields.api_token]
label    = "API Token"
type     = "password"
required = true
```

The host renders the Plugins tab from `config_schema` alone — it doesn't
need to launch the plugin to know what fields the plugin wants.

`api_version` MUST equal Hashpoint's current `sdk.HostAPIVersion`. A
mismatch surfaces with a clear error message; the plugin stays in
`failed` state until rebuilt against the matching SDK.

## Configuration

Plugin configuration lives in the SQLite database (table
`plugin_settings`, columns `plugin_name, key, value, is_secret`). The
Plugins tab in the settings UI is the only supported way to edit it;
the host writes via `Host.SetConfig` / `SetSecret` and reloads the
plugin on every change.

- `text` and `boolean` fields are stored as UTF-8 bytes and delivered
  to the plugin in `PluginConfig.Fields`.
- `password` fields are DPAPI-encrypted before insertion. They are
  bound to the current Windows user account, so copying `data.db` to
  another machine leaves the secrets unreadable. The plugin receives
  an opaque `SecretHandle` it redeems via `HostAPI.RedeemSecret` —
  the cleartext only enters plugin memory at the moment of redemption.

There is also a `plugin_state` table that holds the enable flag per
plugin. Disabled plugins are recorded in `state=disabled` and never
launched; their configuration is preserved across the disable→enable
cycle.

## Lifecycle

1. **Discovery** — at host startup, Hashpoint scans `PluginsDir` for
   subdirectories. A background goroutine re-scans every
   `HostDeps.DiscoveryInterval` (default 30 s) so plugins manually
   dropped into the directory are picked up without an app restart.
   Newly discovered plugins are launched immediately (auto-enable;
   the default `plugin_state.enabled = 1` means no opt-in is needed);
   the host fires the Wails event `plugins:discovered` so the UI
   refreshes live.
2. **Enable check** — for each directory the host reads
   `plugin_state.enabled`; rows with `enabled=0` are recorded as
   `state=disabled` and **skipped** (no subprocess).
3. **Manifest load** — the host parses `manifest.toml` and rejects
   plugins whose name does not equal the directory name or whose
   `api_version` does not equal `sdk.HostAPIVersion`.
4. **Required-field gate** — the host reads `plugin_settings` and
   compares against the manifest. If any required field is unset and
   has no `default`, the plugin is parked in `state=needs_config`
   with the missing keys attached; **no subprocess is launched**.
5. **Launch** — handshake via the magic-cookie protocol.
6. **Init** — host calls `Plugin.Init(host HostAPI)`.
7. **Metadata** — host calls `Plugin.Metadata()` to learn name,
   version, capabilities.
8. **Configure** — host calls `Plugin.Configure(cfg)` with the merged
   field values + fresh SecretHandles.
9. **Use** — capability fan-outs (today: on-call submit) only target
   plugins in `state=running`.
10. **Shutdown** — `Host.Stop()` kills every subprocess; SecretHandles
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

## Pre-installed plugins (MSI seed)

The Windows MSI ships with one plugin bundled out of the box —
[`hashpoint-plugin-manager`](https://github.com/DustHoff/hashpoint-plugin-manager),
the reference implementation of the `plugin_management` capability. It is
laid down under `%ProgramFiles%\Hashpoint\plugins-seed\plugin-manager\`
during installation and copied into the per-user `PluginsDir` the first
time hashpoint runs (see [`internal/plugin/seed.go`](../../internal/plugin/seed.go)).

The seed copy is only performed when the target directory does not yet
exist. That makes user-side updates installed via the in-app Plugins
UI immune to MSI reinstalls — once you bump `plugin-manager` to a newer
version through the UI, no MSI upgrade will roll it back. Deleting the
plugin directory manually triggers a re-seed on the next launch, which
is the recovery path if the user-side install ever ends up corrupt.

The bundled version is pinned to whatever `plugin-manager_*_windows_amd64.zip`
was the latest release in `DustHoff/hashpoint-plugin-manager` at the
moment the MSI was built — see `.github/workflows/release.yml` for the
exact fetch step.

## Plugin sources (`plugin_management`)

A plugin advertising `plugin_management` is itself a *source*: it tells
the host which plugins are available out there, and on user action it
installs / updates / uninstalls plugin bundles by writing files under
`PluginsDir`. See [`api.md`](api.md) for the interface.

The host fans the **Verfügbare Plugins** tab out across every running
source plugin, merges their catalogs, and stamps each row with its
source plugin name + the locally installed version so the UI can pick
between Install, Update (only when versions differ), and Uninstall.

When the user clicks Update, the host stops the target plugin's
subprocess first (Windows holds an exclusive lock on the running `.exe`)
and only then calls `handler.Update`. After a successful Uninstall the
host clears the target's `plugin_state` + `plugin_settings` rows itself
— the handler is only responsible for the bytes on disk. A source
plugin cannot uninstall itself.

## Future capabilities

`oncall_documentation`, `plugin_management`, and `process_autotag` are
the capabilities defined today. Adding new ones is a small SDK change:
a new interface, a new wire-protocol service, an entry in the host's
plugin set. See [`api.md`](api.md) for the pattern.

[hcl]: https://github.com/hashicorp/go-plugin

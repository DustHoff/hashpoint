# ADR 0016 – Plugin-System als Subprozesse (go-plugin, net/rpc) mit Capabilities

- **Status:** Akzeptiert (retrospektiv dokumentiert)
- **Datum:** 2026-09-28
- **Bezug:** Spec §2.6, §2.7; `internal/plugin`, `plugin/sdk`; Migration 0008; `docs/plugins/`

## Kontext

Organisationsspezifische Integrationen (Jira, OTRS, ServiceNow, Feiertagskalender, Auftragskataloge) gehören nicht in den Kern. Fehlerhafte Erweiterungen dürfen den Tracker nicht mitreißen; Plugins sollen unabhängig vom Kern versioniert werden.

## Entscheidung

- Plugins sind **eigenständige Executables**, gestartet als Subprozess über **HashiCorp `go-plugin`** mit **`ProtocolNetRPC`** (kein protoc im Plugin-Build). Der Host besitzt und beendet sie.
- Ablage `%APPDATA%\TimeTracker\plugins\<name>\` mit `manifest.toml` (Name = Verzeichnisname, `api_version == sdk.HostAPIVersion`, `capabilities`, `config_schema`).
- **Capabilities:** `oncall_documentation`, `off_hours_provider`, `process_autotag`, `tag_provider`, `plugin_management`.
- **Lifecycle:** Discovery (Rescan alle 30 s) → Approval → Enable-Check → Manifest → Required-Field-Gate (`needs_config`) → Handshake → `Init`/`Metadata`/`Configure` → `running`; **Crash-Watch** stuft auf `failed` herab und widerruft Secret-Handles.
- Konfiguration in `plugin_settings`/`plugin_state`; UI wird **allein aus dem Manifest** generiert.
- Reverse-RPC **HostAPI**: `RedeemSecret`, `Log`, `RequestEntraToken`, `RequestPersonioSession`, `ListTags`, `PublishTags`.

## Konsequenzen

- ✅ Crash-Isolation; Kern bleibt schlank und organisationsneutral.
- ✅ Plugin-Autoren brauchen nur das SDK.
- ⚠️ Strenge Versionskopplung über `HostAPIVersion` – Plugins müssen bei Bumps neu gebaut werden.
- ⚠️ Plugins laufen mit vollen Benutzerrechten (siehe ADR 0017).

# Architecture Decision Records

Architektur- und Feature-Entscheidungen von Hashpoint. ADRs ab 0002 dokumentieren den **Ist-Stand** retrospektiv (Stand 2026-09-28).

| Nr. | Titel | Bereich |
| --- | --- | --- |
| [0001](0001-collector-ui-process-split.md) | Trennung von Collector- und UI-Prozess | Architektur |
| [0002](0002-lokale-datenhaltung-sqlite.md) | Lokale Datenhaltung in SQLite (pure Go) | Persistenz |
| [0003](0003-fokus-tracking-polling.md) | Fokus-Tracking per Polling mit Idle/Lock/Suspend | Erfassung |
| [0004](0004-trennung-process-tracks-tag-blocks.md) | Trennung process_tracks / tag_blocks | Datenmodell |
| [0005](0005-kommunikations-tracking.md) | Paralleles Kommunikations-Tracking | Erfassung |
| [0006](0006-tag-hierarchie.md) | Zweistufige Tag-Hierarchie mit Hashtag-Schema | Tagging |
| [0007](0007-auto-tagging-orchestrator.md) | Regelbasiertes Auto-Tagging über Orchestrator | Tagging |
| [0008](0008-manuelles-tagging.md) | Manuelles Tagging: Sitzung & Range-Tags | Tagging |
| [0009](0009-quick-tag-hotkey.md) | Quick-Tag-Picker über globalen Hotkey | Bedienung |
| [0010](0010-personio-anbindung-ui-api-cdp.md) | Personio über UI-API mit CDP-Login | Integration |
| [0011](0011-personio-sync-semantik.md) | Personio-Sync: Aggregation, Preflight, Import, Startup-Sync | Integration |
| [0012](0012-tray-bedienung-autostart.md) | Tray-Bedienung, Autostart nur via MSI | Bedienung |
| [0013](0013-timeline-ui.md) | Timeline-UI: Tages- und Monatsansicht | UI |
| [0014](0014-eingebettete-hilfe.md) | Eingebettetes Offline-Handbuch | UI |
| [0015](0015-konfiguration-toml-settings-ui.md) | TOML-Config mit vollständiger Settings-UI | Konfiguration |
| [0016](0016-plugin-system.md) | Plugin-System (go-plugin, Capabilities) | Erweiterbarkeit |
| [0017](0017-plugin-sicherheit.md) | Plugin-Sicherheit: Approval, DPAPI, Handles | Sicherheit |
| [0018](0018-plugin-distribution.md) | Plugin-Distribution: Quellen & MSI-Seed | Erweiterbarkeit |
| [0019](0019-rufbereitschaft-dokumentation.md) | Rufbereitschafts-Dokumentation | Feature |
| [0020](0020-entra-id-anmeldung.md) | Optionale Entra-ID-Anmeldung | Integration |
| [0021](0021-in-app-feedback-github.md) | In-App-Feedback via GitHub | Feature |
| [0022](0022-resilienz-watchdog.md) | Resilienz: Crashguard, Watchdog-Dienst | Betrieb |
| [0023](0023-datenschutz-sicherheits-baseline.md) | Datenschutz- & Sicherheits-Baseline | Sicherheit |
| [0024](0024-build-release-installer.md) | Build, Versionierung, MSI | Delivery |

## Vorlage

```markdown
# ADR NNNN – Titel

- **Status:** Vorgeschlagen | Akzeptiert | Ersetzt durch ADR XXXX
- **Datum:** JJJJ-MM-TT
- **Bezug:** Issues, Spec-Abschnitte, Code

## Kontext
## Entscheidung
## Konsequenzen
## Alternativen (optional)
```

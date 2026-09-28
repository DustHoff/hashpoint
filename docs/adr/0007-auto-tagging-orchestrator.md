# ADR 0007 – Regelbasiertes Auto-Tagging über einen zentralen Orchestrator

- **Status:** Akzeptiert (retrospektiv dokumentiert)
- **Datum:** 2026-09-28
- **Bezug:** Spec §2.4.1, §2.4.1a, §4.4.1; `internal/tagging/{engine,orchestrator}.go`; Migration 0005; `docs/plugins/capability-process-autotag.md`

## Kontext

Wiederkehrende Tätigkeiten (IDE, Ticketsystem, Meetings) sollen ohne Handarbeit getaggt werden. Gleichzeitig müssen manuelle Eingriffe, Kommunikations-Overrides und Plugins konsistent zusammenspielen, ohne die Non-Overlap-Invariante (ADR 0004) zu verletzen.

## Entscheidung

- **Regeln** in `tagging_rules`: `match_field` (`process_name`|`window_title`|`both`), `match_type` (`contains`|`equals`|`regex`), `pattern`, `tag_id`, optionale `description` (≤ 250 Zeichen), `priority` (höher gewinnt), `enabled`.
- Regex ausschließlich mit Go-`regexp` (**RE2**, lineare Laufzeit); ungültige Patterns werden beim Speichern abgelehnt. Live-Test in der UI vor dem Speichern.
- Ein **zentraler Orchestrator** (State-Machine mit `openAuto`, `openManual`, `pausedManual`, `openCommAuto`) ist der einzige Schreiber von `tag_blocks`; der Tracker ruft nur `OnFocusChanged`/`OnFocusCleared`/`OnCommunicationChanged`.
- Gleiche Regel ⇒ Block läuft weiter; andere Regel ⇒ schließen + neu öffnen (Floor-Snap); **Zero-Length-Blöcke** werden unterdrückt.
- **Vorrang-Reihenfolge:** Comm-Auto-Tag > Fokus-Auto-Tag (User-Regel) > `process_autotag`-Plugin (nur wenn keine User-Regel greift, 500 ms Timeout) > pausierte manuelle Sitzung.
- Regel-Description wird auf den Auto-Block übernommen und landet im Personio-Kommentar.

## Konsequenzen

- ✅ Eine einzige, testbare Stelle für den gesamten Block-Lebenszyklus.
- ✅ Kein ReDoS-Risiko durch User-Regex.
- ✅ Plugins erweitern das Auto-Tagging, ohne User-Regeln zu übersteuern.
- ⚠️ Die State-Machine ist komplex; Änderungen erfordern umfangreiche table-driven Tests.
- ⚠️ Plugin-Resolve läuft synchron im Fokus-Loop – daher harter Timeout.

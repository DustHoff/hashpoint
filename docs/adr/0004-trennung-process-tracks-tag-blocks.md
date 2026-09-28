# ADR 0004 – Trennung von Roh-Erfassung (process_tracks) und Tagging (tag_blocks)

- **Status:** Akzeptiert (retrospektiv dokumentiert)
- **Datum:** 2026-09-28
- **Bezug:** Spec §2.1, §2.4.3, §4.3; Migration 0004 (`split_blocks`)

## Kontext

Ursprünglich vermischte `focus_blocks` Prozess-Aktivität und Tagging-Status. Das führte zu Konflikten: Granularitäts-Snapping verfälschte die Rohdaten, und manuelles Tagging über Zeiträume ließ sich nicht sauber auf einzelne Fokus-Events abbilden.

## Entscheidung

- Zwei unabhängige Tabellen:
  - **`process_tracks`** – rohe, sekundengenaue Fokus-Events, **ohne** Tags und **ohne** Granularität. Read-only in der UI.
  - **`tag_blocks`** – Tagging-Spannen (`tag_id`, `description`, `is_manual`, `personio_id`, `synced_at`), gepflegt ausschließlich vom Tagging-Orchestrator.
- **Granularität** (`tag_block_granularity_min`, 0 = aus) wirkt **nur** auf `tag_blocks`, lokal-zeitausgerichtet ab Mitternacht.
- **Non-Overlap-Invariante** auf `tag_blocks`: jedes Open/SetEnd/SetStart/Resize prüft Überschneidungen (offene Blöcke gelten bis ∞) und liefert `ErrOverlap`.
- Migration 0004 überführt Bestandsdaten in beide Tabellen; die Down-Migration führt sie wieder zusammen.

## Konsequenzen

- ✅ Rohdaten bleiben unverfälscht nachvollziehbar (Bottom-Strip der Timeline).
- ✅ Personio-Sync kann direkt auf `tag_blocks` aggregieren; die Non-Overlap-Invariante verhindert Personios `400 – overlapping WORK periods`.
- ⚠️ Zwei Lebenszyklen (Tracker vs. Orchestrator) müssen beim Start getrennt recovert werden (`Recover`, `CloseDanglingManualAtStartup`).

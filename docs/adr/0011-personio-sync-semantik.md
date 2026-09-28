# ADR 0011 – Personio-Sync: Aggregation, Idempotenz, Preflight/Import und Startup-Sync

- **Status:** Akzeptiert (retrospektiv dokumentiert)
- **Datum:** 2026-09-28
- **Bezug:** Spec §2.5.3–§2.5.7, §4.5; `internal/personio/{sync,import}.go`, `SyncConflictModal.tsx`

## Kontext

Getaggte Zeit muss als Arbeitszeit-Perioden nach Personio. `PUT day` ersetzt einen Tag vollständig – manuelle Eingaben in Personio würden stillschweigend verloren gehen. Ein Sync beim Herunterfahren scheiterte regelmäßig, weil Windows das Netz vorher trennt.

## Entscheidung

- **Quelle:** geschlossene `tag_blocks` mit effektivem `sync_to_personio = 1`; keine zusätzliche Rundung.
- **Aggregation:** konsekutive Blöcke mit gleichem (lokalem Datum, `project_id`, Kommentar) und Lücke ≤ 5 s ⇒ eine Period (`period_type = work`). Kommentar: `<parent> <sub> <sub_description>` + optional ` — <block_description>`.
- **Idempotenz:** `PUT day` pro Tag; synchronisierte Blöcke erhalten `synced_at` + `personio_id` (= `day_id`). Fehlende `day_id` für trackbare Tage wird clientseitig als UUID v4 erzeugt.
- **Preflight:** vor jedem Push wird das Timesheet gelesen. Existieren Work-Perioden, entscheidet der Anwender per Modal: **Überschreiben**, **Aus Personio importieren** oder **Abbrechen**.
- **Import:** Personio-Perioden werden als manuelle Blöcke übernommen, lokale Blöcke gewinnen (`subtractRanges`), kein Snapping. Tag-Auflösung: Kommentar-Schema `#Parent #Sub` (`EnsureByPath`) → `project_id` → Fallback-Tag `#PersonioImport` (`sync_to_personio = 0`).
- **Startup-Sync** statt Shutdown-Sync: beim Start wird asynchron der **letzte unsynchronisierte Tag vor heute** synchronisiert (30 s Timeout), Ergebnis als Banner; Konflikte lösen dasselbe Modal aus.

## Konsequenzen

- ✅ Kein unbemerkter Datenverlust in Personio; Round-Trip Export ↔ Import ist verlustarm.
- ✅ Wochenenden/Urlaub werden übersprungen, ohne Tage zu verpassen.
- ⚠️ Pausen werden nicht synchronisiert; Personio generiert sie selbst.
- ⚠️ Ohne gültige Session läuft der Startup-Sync stillschweigend nicht.

# ADR 0013 – Timeline-UI: Zwei-Strip-Tagesansicht und Monatsübersicht

- **Status:** Akzeptiert (retrospektiv dokumentiert)
- **Datum:** 2026-09-28
- **Bezug:** Spec §2.3; `frontend/src/components/{Timeline,MonthCalendar}.tsx`; `docs/user/zeiterfassung.md`

## Kontext

Anwender müssen Rohaktivität und Tagging gleichzeitig sehen, um Lücken zu erkennen und nachzutaggen. Für Monatsabschlüsse wird zusätzlich ein Überblick über Sync-Status und getaggte Stunden benötigt.

## Entscheidung

- Stack: **Wails v2** mit **React + TypeScript (strict) + Vite + Tailwind**; alle Backend-Aufrufe ausschließlich über `frontend/src/api/`.
- **Tagesansicht** (Fenster startet maximiert):
  - **Top-Strip** – `tag_blocks` (Auto gestrichelt, manuell ohne Rand); einzige Stelle für Drag-to-tag, Selektion, Resize.
  - **Bottom-Strip** – `process_tracks` read-only, deterministische Farbe pro Prozess, Idle abgeblendet, eigene Schiene für Kommunikations-Tracks.
  - Gemeinsame Zoom/Scroll-Achse (Mausrad zoomt cursor-verankert, Shift schwenkt, Doppelklick reset).
  - Darunter zwei Tabellen 40/60 (Tag-Blöcke | gruppierte Prozesse); Hover filtert die Prozess-Tabelle.
- **Monatsansicht** (Umschalter Tag | Monat): 6×7-Raster, getaggte Summe, Tag-Farbleiste, **Sync-Status-Symbol** pro Tag (✓ / ◐ / ⊘), Klick = Drilldown in den Tag.
- Eigene Timeline-Komponente statt `vis-timeline`/`react-calendar-timeline`.

## Konsequenzen

- ✅ Rohdaten und Tagging sind direkt vergleichbar; Nachtaggen ohne Kontextwechsel.
- ✅ Monatsabschluss auf einen Blick.
- ⚠️ Eigene Komponente = eigener Wartungsaufwand; Performance-Ziel < 500 ms bei > 200 Blöcken.

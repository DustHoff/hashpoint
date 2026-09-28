# ADR 0005 – Paralleles Kommunikations-Tracking (Meetings/Calls)

- **Status:** Akzeptiert (retrospektiv dokumentiert)
- **Datum:** 2026-09-28
- **Bezug:** Spec §2.1a, §2.4.1a; Migration 0006; `[communication]`-Config

## Kontext

Wer in Teams/Zoom an einem Meeting teilnimmt, arbeitet oft parallel in anderen Fenstern. Reines Fokus-Tracking würde die Meeting-Zeit der Regel des fokussierten Fensters zuordnen – fachlich falsch. Zudem erzeugt passives Zuhören keinen Input, Idle-Detection würde die Erfassung beenden.

## Entscheidung

- Zusätzlich zum Fokus erfasst der Tracker **Kommunikations-Tracks** in derselben Tabelle `process_tracks` mit `is_communication = 1`.
- Erkennung: **sichtbares Top-Level-Fenster** eines konfigurierten Prozesses (`communication.process_names`, Default `teams.exe`), dessen Titel **keine** der `title_exclude_phrases` enthält (case-insensitiver Substring, in jedem Tick neu geprüft).
- Ein offener Track pro (PID, HWND); Titelwechsel ⇒ neuer Track; Fenster weg ⇒ sofort schließen.
- **Idle und Lock wirken nicht** auf Kommunikations-Tracks; `Pause` schließt sie jedoch.
- Kommunikations-Tracks dürfen Fokus-Tracks und einander überlappen; Darstellung auf eigener Timeline-Schiene.
- Matcht eine Auto-Tag-Regel ein Kommunikations-Fenster, hat dieser **Comm-Auto-Block absoluten Vorrang** vor Fokus-Auto-Tags (ADR 0007).
- Beide Listen sind in der Settings-UI hot-reloadable.

## Konsequenzen

- ✅ Meeting-Zeit wird korrekt zugeordnet, auch bei Nebenbei-Arbeit.
- ✅ Hintergrund-Fenster (Teams im Tray) erzeugen keine Tracks.
- ⚠️ Overlap-freiheit gilt nur für Fokus-Tracks; Auswertungen müssen `is_communication` beachten.
- ⚠️ Chat-Fenster etc. müssen über Ausschluss-Phrasen gefiltert werden.

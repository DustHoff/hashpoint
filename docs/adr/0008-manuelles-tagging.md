# ADR 0008 – Manuelles Tagging: offene Sitzung und Range-Tags („Manual schlägt Auto“)

- **Status:** Akzeptiert (retrospektiv dokumentiert)
- **Datum:** 2026-09-28
- **Bezug:** Spec §2.3, §2.3.1, §2.4.2, §2.4.3

## Kontext

Nicht jede Tätigkeit lässt sich per Regel erkennen. Anwender brauchen zwei Arten manueller Zuordnung: einen **laufenden Default-Kontext** („ich arbeite jetzt an Projekt X“) und eine **nachträgliche Korrektur** vergangener Zeiträume.

## Entscheidung

- **Offene manuelle Sitzung** (Tray-Submenü, Quick-Tag-Picker): genau **ein** offener manueller Block zur Zeit, ohne Ende-Datum.
  - Eine greifende Auto-Regel **pausiert** die Sitzung (Tag + Beschreibung werden gemerkt); nach Ende des Auto-Blocks wird ein **neuer** manueller Block mit gleichem Tag/Beschreibung fortgesetzt (bei Idle erst beim nächsten Fokus-Event).
  - Start während eines Auto-Blocks ⇒ Sitzung wird nur „pausiert“ vorgemerkt.
  - Dangling offene Sitzungen werden beim Start geschlossen (`CloseDanglingManualAtStartup`).
- **Range-Tags** per Drag im Top-Strip der Timeline (Start floor, Ende ceil):
  - überlappende **Auto-Blöcke** werden gelöscht, getrimmt oder gesplittet;
  - Überschneidung mit **manuellen** Blöcken ⇒ `ErrOverlap` (inkl. laufender Sitzung).
- Bestehende Blöcke: Re-Tag, Beschreibung, Löschen und **Resize** (hartes Clamping an Nachbarn, Snap aufs Raster, Auto-Block wird dabei zu `is_manual = 1`; offene Blöcke nicht resizable).
- Jeder Block hat eine optionale **Tätigkeitsbeschreibung**, die in den Personio-Kommentar übernommen wird.

## Konsequenzen

- ✅ Anwender behält die Kontrolle; manuelle Eingriffe werden von Auto-Tagging nie überschrieben.
- ✅ Kombination aus Default-Kontext und Auto-Unterbrechungen ergibt lückenarme Tage.
- ⚠️ Eine Range während laufender Sitzung wird abgelehnt – die Sitzung muss erst gestoppt werden.

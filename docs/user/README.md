# Hashpoint TimeTracker – Benutzerhandbuch

Willkommen im Benutzerhandbuch des Hashpoint TimeTrackers. Diese Dokumentation richtet sich an Endanwender und beschreibt, wie der TimeTracker eingerichtet und im Alltag bedient wird.

## Was ist der TimeTracker?

Der TimeTracker ist eine Windows-Desktop-Anwendung, die im Hintergrund automatisch erfasst, in welchen Anwendungen und Fenstern Sie arbeiten. Die App pflegt zwei Schichten:

- **Process-Tracks** — die rohe Fokus-Aktivität (was Sie tatsächlich auf dem Bildschirm hatten, sekundengenau).
- **Tag-Blöcke** — Tagging-Spannen, die diese Aktivität in Kategorien einsortieren. Sie entstehen automatisch durch Auto-Tagging-Regeln oder manuell (per Drag auf der Zeitachse oder über das Tray-Submenü) und sind die Quelle für die Personio-Synchronisation.

## Inhalt

| Kapitel | Inhalt |
| --- | --- |
| [Installation & Erste Schritte](installation.md) | Speicherorte, Autostart, erster Start |
| [Einstellungen](einstellungen.md) | Konfiguration via Settings-Tab, `config.toml` und Credential Manager |
| [Zeitachse & Zeiterfassung](zeiterfassung.md) | Tagesansicht, Blöcke bearbeiten, Pause/Fortsetzen, Bereichs-Tagging |
| [Tags verwalten](tags.md) | Tag-Hierarchie, Farben, Personio-Mappings |
| [Auto-Tagging-Regeln](auto-tagging.md) | Regeln definieren, testen und auf Historie anwenden |
| [Personio-Synchronisation](personio.md) | Voraussetzungen, Sync-Logik, Fehlerbehandlung |
| [Systemtray](tray.md) | Tray-Menü, Pause, manuelles Tagging, Autostart, Beenden |
| [Quick-Tag-Picker](quick-tag.md) | Globaler Hotkey für blitzschnelles manuelles Taggen |

## Schnellstart in fünf Schritten

1. **Anwendung starten** – Der TimeTracker erscheint im Systemtray (Benachrichtigungsbereich).
2. **Tags anlegen** – Im Tab **Tags** mindestens einen Eltern-Tag mit Personio-Projekt-/Aktivitäts-ID anlegen.
3. **Auto-Tagging einrichten** *(optional)* – Im Tab **Auto-Tagging** Regeln für häufig genutzte Anwendungen definieren.
4. **Tagesverlauf taggen** – Im Tab **Zeitachse** ungetaggte Blöcke markieren und einem Tag zuweisen.
5. **An Personio synchronisieren** – Über die Schaltfläche **Sync zu Personio** den aktuellen Tag übertragen.

## Wo finde ich was?

- **Tray-Icon (rechts unten):** Pause umschalten, manuelles Tagging,
  Sync zu Personio, Autostart, Beenden
- **Hauptfenster:** Fünf Tabs – Zeitachse, Tags, Auto-Tagging,
  Einstellungen, Über; oben rechts der Personio-Status-Badge
- **Konfigurationsdatei:** `%APPDATA%\TimeTracker\config.toml`
- **Datenbank & Logs:** `%LOCALAPPDATA%\TimeTracker\`
- **Personio-Session:** Windows Credential Manager
  (`TimeTracker.PersonioSession`)

# Hashpoint TimeTracker – Benutzerhandbuch

Willkommen im Benutzerhandbuch des Hashpoint TimeTrackers. Diese Dokumentation richtet sich an Endanwender und beschreibt, wie der TimeTracker eingerichtet und im Alltag bedient wird.

## Was ist der TimeTracker?

Der TimeTracker ist eine Windows-Desktop-Anwendung, die im Hintergrund automatisch erfasst, in welchen Anwendungen und Fenstern Sie arbeiten. Aus diesen Erfassungen entstehen sogenannte **Fokus-Blöcke**, die sich anschließend kategorisieren (taggen) und an Personio synchronisieren lassen.

## Inhalt

| Kapitel | Inhalt |
| --- | --- |
| [Installation & Erste Schritte](installation.md) | Speicherorte, Autostart, erster Start |
| [Einstellungen](einstellungen.md) | Konfiguration via `config.toml` und Credential Manager |
| [Zeitachse & Zeiterfassung](zeiterfassung.md) | Tagesansicht, Blöcke bearbeiten, Pause/Fortsetzen |
| [Tags verwalten](tags.md) | Tag-Hierarchie, Farben, Personio-Mappings |
| [Auto-Tagging-Regeln](auto-tagging.md) | Regeln definieren, testen und auf Historie anwenden |
| [Personio-Synchronisation](personio.md) | Voraussetzungen, Sync-Logik, Fehlerbehandlung |
| [Systemtray](tray.md) | Tray-Menü, Pause, Autostart, Beenden |

## Schnellstart in fünf Schritten

1. **Anwendung starten** – Der TimeTracker erscheint im Systemtray (Benachrichtigungsbereich).
2. **Tags anlegen** – Im Tab **Tags** mindestens einen Eltern-Tag mit Personio-Projekt-/Aktivitäts-ID anlegen.
3. **Auto-Tagging einrichten** *(optional)* – Im Tab **Auto-Tagging** Regeln für häufig genutzte Anwendungen definieren.
4. **Tagesverlauf taggen** – Im Tab **Zeitachse** ungetaggte Blöcke markieren und einem Tag zuweisen.
5. **An Personio synchronisieren** – Über die Schaltfläche **Sync zu Personio** den aktuellen Tag übertragen.

## Wo finde ich was?

- **Tray-Icon (rechts unten):** Pause umschalten, Autostart, Beenden
- **Hauptfenster:** Vier Tabs – Zeitachse, Tags, Auto-Tagging, Über
- **Konfigurationsdatei:** `%APPDATA%\TimeTracker\config.toml`
- **Datenbank & Logs:** `%LOCALAPPDATA%\TimeTracker\`

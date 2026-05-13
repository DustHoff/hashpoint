# Hashpoint TimeTracker – Benutzerhandbuch

Willkommen im Benutzerhandbuch des Hashpoint TimeTrackers. Diese Dokumentation richtet sich an Endanwender und beschreibt, wie der TimeTracker eingerichtet und im Alltag bedient wird.

## Was ist der TimeTracker?

Der TimeTracker ist eine Windows-Desktop-Anwendung, die im Hintergrund automatisch erfasst, in welchen Anwendungen und Fenstern Sie arbeiten. Die App pflegt zwei Schichten:

- **Process-Tracks** — die rohe Fokus-Aktivität (was Sie tatsächlich auf dem Bildschirm hatten, sekundengenau).
- **Kommunikations-Tracks** — parallel zur Fokus-Erfassung laufende Tracks für Programme wie Teams, Zoom oder Slack, sobald diese ein sichtbares Fenster anzeigen. So fallen Meetings und Bildschirmfreigaben auch dann nicht unter den Tisch, wenn Sie nebenbei in einem anderen Fenster arbeiten.
- **Tag-Blöcke** — Tagging-Spannen, die diese Aktivität in Kategorien einsortieren. Sie entstehen automatisch durch Auto-Tagging-Regeln oder manuell (per Drag auf der Zeitachse oder über das Tray-Submenü) und sind die Quelle für die Personio-Synchronisation. Auto-Tag-Regeln, die auf einen Kommunikations-Prozess matchen, übersteuern konkurrierende Auto-Tags aus dem Vordergrund-Prozess.

## Inhalt

| Kapitel | Inhalt |
| --- | --- |
| [Installation & Erste Schritte](installation.md) | Speicherorte, Autostart, erster Start |
| [Einstellungen](einstellungen.md) | Konfiguration via Settings-Tab, `config.toml` und Credential Manager |
| [Zeitachse & Zeiterfassung](zeiterfassung.md) | Tages- und Monatsansicht, Blöcke bearbeiten, Pause/Fortsetzen, Bereichs-Tagging |
| [Tags verwalten](tags.md) | Tag-Hierarchie, Farben, Personio-Mappings |
| [Auto-Tagging-Regeln](auto-tagging.md) | Regeln definieren, testen und auf Historie anwenden |
| [Personio-Synchronisation](personio.md) | Voraussetzungen, Sync-Logik, Fehlerbehandlung |
| [Microsoft Entra ID](entra-id.md) | Optionale Anmeldung für Microsoft 365, SharePoint, Kalender, Custom-APIs |
| [Systemtray](tray.md) | Tray-Menü, Pause, manuelles Tagging, Beenden |
| [Quick-Tag-Picker](quick-tag.md) | Globaler Hotkey für blitzschnelles manuelles Taggen |

## Schnellstart in fünf Schritten

1. **Anwendung starten** – Der TimeTracker erscheint im Systemtray (Benachrichtigungsbereich).
2. **Tags anlegen** – Im Tab **Tags** mindestens einen Eltern-Tag mit Personio-Projekt-/Aktivitäts-ID anlegen.
3. **Auto-Tagging einrichten** *(optional)* – Im Tab **Auto-Tagging** Regeln für häufig genutzte Anwendungen definieren.
4. **Tagesverlauf taggen** – Im Tab **Zeitachse** ungetaggte Blöcke markieren und einem Tag zuweisen.
5. **An Personio synchronisieren** – Über die Schaltfläche **Sync zu Personio** den aktuellen Tag übertragen.

## Wo finde ich was?

- **Tray-Icon (rechts unten):** Pause umschalten, manuelles Tagging,
  Sync zu Personio, Beenden
- **Hauptfenster:** Sechs Tabs – Zeitachse, Tags, Auto-Tagging,
  Einstellungen, Hilfe, Über; oben rechts der Personio-Status-Badge.
  Der Tab **Hilfe** zeigt das eingebettete Benutzerhandbuch (auch
  direkt aus dem Tray über den Eintrag *Hilfe* erreichbar).
- **Konfigurationsdatei:** `%APPDATA%\TimeTracker\config.toml`
- **Datenbank & Logs:** `%LOCALAPPDATA%\TimeTracker\`
- **Personio-Session:** Windows Credential Manager
  (`TimeTracker.PersonioSession`)
- **Entra-ID-Token-Cache:** `%LOCALAPPDATA%\TimeTracker\auth\msal_cache.bin`
  (DPAPI-verschlüsselt)

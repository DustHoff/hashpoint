# Installation & Erste Schritte

## Systemvoraussetzungen

- Windows 10 oder Windows 11 (x64)
- Schreibrechte auf das eigene Benutzerprofil (`%APPDATA%` und `%LOCALAPPDATA%`)
- Für Personio-Sync: gültige Personio-API-Zugangsdaten (Client ID, Client Secret, Employee ID)

## Erster Start

1. Anwendung über das Setup oder die ausgelieferte `.exe` starten.
2. Beim ersten Start wird automatisch angelegt:
   - `%APPDATA%\TimeTracker\config.toml` – Konfiguration mit Standardwerten
   - `%LOCALAPPDATA%\TimeTracker\data.db` – SQLite-Datenbank
   - `%LOCALAPPDATA%\TimeTracker\log\` – Log-Verzeichnis
3. Im Systemtray (Benachrichtigungsbereich rechts unten) erscheint das Hashpoint-Symbol.
4. Per Klick auf das Tray-Icon öffnet sich das Hauptfenster.

Sobald die Anwendung läuft, erfasst sie automatisch jede Sekunde, welches Fenster im Vordergrund ist. Es ist keine zusätzliche Aktivierung nötig.

## Speicherorte

| Pfad | Inhalt |
| --- | --- |
| `%APPDATA%\TimeTracker\config.toml` | Konfigurationsdatei (TOML) |
| `%LOCALAPPDATA%\TimeTracker\data.db` | SQLite-Datenbank mit Blöcken, Tags und Regeln |
| `%LOCALAPPDATA%\TimeTracker\log\*.log` | Strukturierte JSON-Logs |
| Windows Credential Manager | Personio Client Secret (verschlüsselt) |

> Die Verzeichnisse werden mit Benutzer-only-Rechten (`0o700`) angelegt. Andere Benutzer am Gerät haben keinen Zugriff.

## Autostart

Damit der TimeTracker beim Anmelden automatisch startet, gibt es zwei Wege:

1. **Tray-Menü:** Rechtsklick auf das Tray-Icon → Eintrag **Autostart** anhaken.
2. **Konfigurationsdatei:** In `config.toml` im Abschnitt `[ui]` den Wert `autostart = true` setzen.

Beide Wege schreiben denselben Eintrag in die Windows-Registry. Wird Autostart deaktiviert, wird der Registry-Eintrag wieder entfernt.

## Beenden

Die Anwendung läuft auch dann weiter, wenn das Hauptfenster geschlossen wird (sie minimiert sich in den Tray). Vollständig beenden:

- Rechtsklick auf das Tray-Icon → **Beenden**

Beim Beenden wird ein offener Block automatisch sauber abgeschlossen, damit keine Erfassung "hängen" bleibt.

## Datensicherung

Zur Sicherung Ihrer Erfassung genügt es, folgende Pfade zu kopieren:

- `%LOCALAPPDATA%\TimeTracker\` – komplette Datenbank inklusive Tags und Regeln
- `%APPDATA%\TimeTracker\config.toml` – Konfiguration

Das Personio Client Secret liegt im Windows Credential Manager und wird durch eine Datei-Sicherung **nicht** mit übertragen. Bei einem Geräte-Wechsel muss es einmal neu hinterlegt werden.

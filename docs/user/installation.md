# Installation & Erste Schritte

## Systemvoraussetzungen

- Windows 10 oder Windows 11 (x64)
- Schreibrechte auf das eigene Benutzerprofil (`%APPDATA%` und `%LOCALAPPDATA%`)
- Für Personio-Sync:
  - Eigenes Personio-Login (E-Mail/Passwort, ggf. MFA/SSO) — der TimeTracker
    benötigt **keine** firmenweiten API-Credentials.
  - Lokal installierter **Google Chrome**, da die Anmeldung über das
    Chrome DevTools Protocol (CDP) läuft. Edge oder die Wails-WebView
    werden nicht unterstützt.
  - Tenant-Subdomain (z. B. `onesi` für `https://onesi.personio.de`).

## Installationswege

Jedes GitHub-Release liefert zwei Artefakte aus, dazu eine `checksums.txt` mit SHA-256-Hashes für beide:

| Datei | Empfohlen für | Anmerkung |
| --- | --- | --- |
| `hashpoint-<version>.msi` | Standard-Installation auf einem Arbeitsplatz und für unbeaufsichtigte IT-Roll-Outs | WiX-MSI, **per-machine** (UAC-Prompt). Legt Startmenü-Eintrag und Autostart an. |
| `hashpoint.exe` | Portable Nutzung, Tests, manuelle Updates | Single-File-Build ohne Installation. Kein Startmenü-Eintrag, Autostart muss im Tray manuell aktiviert werden. |

### MSI-Installer (empfohlen)

1. `hashpoint-<version>.msi` herunterladen.
2. Doppelklick → Windows Installer öffnet sich, **UAC-Bestätigung** akzeptieren.
3. Hashpoint wird nach `C:\Program Files\Hashpoint\` installiert; ein Eintrag **Hashpoint TimeTracker** erscheint im Startmenü.
4. **Autostart** wird für den installierenden User direkt aktiviert (HKCU-Run-Eintrag).
5. Programm über das Startmenü starten.

**Silent-Installation für IT-Verteilung:**

```
msiexec /i hashpoint-<version>.msi /quiet
```

Optional mit Logging:

```
msiexec /i hashpoint-<version>.msi /quiet /log %TEMP%\hashpoint-install.log
```

> **Hinweis zum Autostart bei MSI-Installation:** Der Installer setzt den Autostart-Registry-Eintrag (`HKCU\Software\Microsoft\Windows\CurrentVersion\Run\HashpointTimeTracker`) für den Account, der den Installer ausführt. Andere Benutzer desselben Geräts müssen den Autostart bei Bedarf über das **Tray-Menü → Autostart** selbst aktivieren — bei einem System-/SCCM-Roll-Out (Installer läuft als `SYSTEM`) ist das immer der Fall.

**Update:** Eine neuere MSI einfach drüber installieren — der Installer erkennt die alte Version und ersetzt sie sauber (`MajorUpgrade`). Eine Downgrade-Installation wird abgelehnt.

**Deinstallation:** Über *Apps & Features* → *Hashpoint TimeTracker* → *Deinstallieren*. Datenbank und Konfiguration in `%LOCALAPPDATA%\TimeTracker\` und `%APPDATA%\TimeTracker\` bleiben erhalten und müssen bei Bedarf manuell entfernt werden.

### Portable EXE

1. `hashpoint.exe` an einen beliebigen Ort kopieren (z. B. `%USERPROFILE%\Tools\Hashpoint\`).
2. Doppelklick startet die Anwendung.
3. Autostart bei Bedarf im Tray-Menü aktivieren.

### Erster Start

1. Beim ersten Start wird automatisch angelegt:
   - `%APPDATA%\TimeTracker\config.toml` – Konfiguration mit Standardwerten
   - `%LOCALAPPDATA%\TimeTracker\data.db` – SQLite-Datenbank
   - `%LOCALAPPDATA%\TimeTracker\log\` – Log-Verzeichnis
2. Im Systemtray (Benachrichtigungsbereich rechts unten) erscheint das Hashpoint-Symbol.
3. Per Klick auf das Tray-Icon öffnet sich das Hauptfenster.

Sobald die Anwendung läuft, erfasst sie automatisch jede Sekunde, welches Fenster im Vordergrund ist. Es ist keine zusätzliche Aktivierung nötig.

## Speicherorte

| Pfad | Inhalt |
| --- | --- |
| `%APPDATA%\TimeTracker\config.toml` | Konfigurationsdatei (TOML) |
| `%LOCALAPPDATA%\TimeTracker\data.db` | SQLite-Datenbank mit Blöcken, Tags und Regeln |
| `%LOCALAPPDATA%\TimeTracker\log\*.log` | Strukturierte JSON-Logs |
| Windows Credential Manager (`TimeTracker.PersonioSession`) | Personio-Session-Cookies (verschlüsselt) |

> Die Verzeichnisse werden mit Benutzer-only-Rechten (`0o700`) angelegt. Andere Benutzer am Gerät haben keinen Zugriff.

## Autostart

Damit der TimeTracker beim Anmelden automatisch startet, gibt es drei Wege:

1. **MSI-Installer:** Der Installer aktiviert den Autostart automatisch für den installierenden User (siehe Abschnitt **MSI-Installer**).
2. **Tray-Menü:** Rechtsklick auf das Tray-Icon → Eintrag **Autostart** anhaken.
3. **Konfigurationsdatei:** In `config.toml` im Abschnitt `[ui]` den Wert `autostart = true` setzen.

Alle drei Wege schreiben denselben Eintrag (`HKCU\Software\Microsoft\Windows\CurrentVersion\Run\HashpointTimeTracker`). Wird Autostart über Tray oder Config deaktiviert, wird der Registry-Eintrag wieder entfernt — auch wenn er ursprünglich vom MSI-Installer angelegt wurde.

## Beenden

Die Anwendung läuft auch dann weiter, wenn das Hauptfenster geschlossen wird (sie minimiert sich in den Tray). Vollständig beenden:

- Rechtsklick auf das Tray-Icon → **Beenden**

Beim Beenden wird ein offener Block automatisch sauber abgeschlossen, damit keine Erfassung "hängen" bleibt.

## Datensicherung

Zur Sicherung Ihrer Erfassung genügt es, folgende Pfade zu kopieren:

- `%LOCALAPPDATA%\TimeTracker\` – komplette Datenbank inklusive Tags und Regeln
- `%APPDATA%\TimeTracker\config.toml` – Konfiguration

Die Personio-Session-Cookies liegen im Windows Credential Manager und werden
durch eine Datei-Sicherung **nicht** mit übertragen. Bei einem Geräte-Wechsel
genügt eine erneute interaktive Anmeldung im Personio-Bereich der Einstellungen.

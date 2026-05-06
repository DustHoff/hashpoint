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
| `hashpoint.exe` | Portable Nutzung, Tests, manuelle Updates | Single-File-Build ohne Installation. Kein Startmenü-Eintrag und kein Autostart — wer Autostart wünscht, legt den Run-Eintrag manuell an (siehe Abschnitt **Autostart**). |

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

> **Hinweis zum Autostart bei MSI-Installation:** Der Installer setzt den Autostart-Registry-Eintrag (`HKCU\Software\Microsoft\Windows\CurrentVersion\Run\HashpointTimeTracker`) für den Account, der den Installer ausführt. Andere Benutzer desselben Geräts (oder ein System-/SCCM-Roll-Out, bei dem der Installer als `SYSTEM` läuft) erhalten den Autostart **nicht** automatisch — der Run-Eintrag muss bei Bedarf manuell für das jeweilige Profil angelegt werden (siehe Abschnitt **Autostart**).

**Update:** Eine neuere MSI einfach drüber installieren — der Installer erkennt die alte Version und ersetzt sie sauber (`MajorUpgrade`). Eine Downgrade-Installation wird abgelehnt.

**Deinstallation:** Über *Apps & Features* → *Hashpoint TimeTracker* → *Deinstallieren*. Datenbank und Konfiguration in `%LOCALAPPDATA%\TimeTracker\` und `%APPDATA%\TimeTracker\` bleiben erhalten und müssen bei Bedarf manuell entfernt werden.

### Portable EXE

1. `hashpoint.exe` an einen beliebigen Ort kopieren (z. B. `%USERPROFILE%\Tools\Hashpoint\`).
2. Doppelklick startet die Anwendung.
3. Autostart bei Bedarf manuell einrichten (siehe Abschnitt **Autostart**).

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

Den Autostart richtet ausschließlich der **MSI-Installer** ein — er legt für den installierenden Account den Registry-Eintrag

```
HKCU\Software\Microsoft\Windows\CurrentVersion\Run\HashpointTimeTracker = "<install-pfad>\hashpoint.exe"
```

an. Die Anwendung selbst bietet keinen Autostart-Toggle mehr (weder im Tray-Menü noch in den Einstellungen).

**Autostart nachträglich aktivieren** (z. B. nach Portable-EXE-Nutzung, bei zusätzlichen Profilen am gleichen Gerät oder nach einem System-Roll-Out als `SYSTEM`): den oben genannten Run-Eintrag manuell für das eigene Benutzerprofil anlegen, etwa per PowerShell:

```powershell
$exe = "C:\Program Files\Hashpoint\hashpoint.exe"   # bzw. eigener Portable-Pfad
New-ItemProperty -Path "HKCU:\Software\Microsoft\Windows\CurrentVersion\Run" `
                 -Name "HashpointTimeTracker" -Value "`"$exe`"" -PropertyType String -Force | Out-Null
```

**Autostart deaktivieren:** den Run-Eintrag wieder entfernen — z. B. über *Task-Manager → Autostart* (Eintrag „HashpointTimeTracker" → *Deaktivieren*) oder per `Remove-ItemProperty` auf demselben Registry-Pfad. Das Deinstallieren der MSI entfernt den Eintrag ebenfalls.

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

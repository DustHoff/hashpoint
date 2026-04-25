# Einstellungen

Aktuell besitzt der TimeTracker **kein** Einstellungs-Dialog im Hauptfenster. Alle technischen Einstellungen werden direkt in der Konfigurationsdatei vorgenommen. Personio-Zugangsdaten werden zusätzlich im Windows Credential Manager hinterlegt.

## Konfigurationsdatei `config.toml`

**Pfad:** `%APPDATA%\TimeTracker\config.toml`

Beim ersten Start wird die Datei automatisch mit Standardwerten erzeugt. Sie kann mit jedem Texteditor (z. B. Notepad, VS Code, Notepad++) bearbeitet werden. Nach einer Änderung ist ein **Neustart der Anwendung** erforderlich.

### Beispiel-Konfiguration

```toml
[tracking]
poll_interval_sec = 2
idle_threshold_min = 5

[personio]
client_id = ""
employee_id = ""
base_url = "https://api.personio.de/v1"

[ui]
autostart = true
```

### Abschnitt `[tracking]` – Erfassung

| Feld | Typ | Default | Wertebereich | Bedeutung |
| --- | --- | --- | --- | --- |
| `poll_interval_sec` | Integer | `2` | `1`–`30` | Wie häufig (in Sekunden) der TimeTracker prüft, welches Fenster im Vordergrund ist. Niedrigere Werte = präzisere Erfassung, höhere CPU-Last. |
| `idle_threshold_min` | Integer | `5` | `1`–`240` | Nach wie vielen Minuten ohne Tastatur- oder Maus-Eingabe der aktuelle Block als **Idle** markiert und beendet wird. |

### Abschnitt `[personio]` – Personio-Anbindung

| Feld | Typ | Default | Bedeutung |
| --- | --- | --- | --- |
| `client_id` | String | `""` | OAuth2-Client-ID für die Personio-API. Pflichtfeld für Sync. |
| `employee_id` | String | `""` | Eigene Personio-Mitarbeiter-ID, für die Anwesenheiten gebucht werden. Pflichtfeld für Sync. |
| `base_url` | String | `https://api.personio.de/v1` | Basis-URL der Personio-API. Nur ändern, wenn von Personio explizit eine andere URL vorgegeben wird. |

> Das **Client Secret** liegt **nicht** in der Konfigurationsdatei, sondern verschlüsselt im Windows Credential Manager. Siehe Abschnitt unten.

### Abschnitt `[ui]` – Oberfläche

| Feld | Typ | Default | Bedeutung |
| --- | --- | --- | --- |
| `autostart` | Boolean | `true` | Startet die Anwendung automatisch beim Windows-Login. Lässt sich auch im Tray-Menü umschalten. |

## Validierung

Beim Laden der Konfiguration werden die Werte geprüft. Schlägt die Validierung fehl, startet der TimeTracker **nicht** und gibt eine klare Fehlermeldung im Log aus. Häufige Fehler:

- `poll_interval_sec` außerhalb `[1, 30]`
- `idle_threshold_min` außerhalb `[1, 240]`
- `personio.base_url` ist leer

In diesem Fall die Datei korrigieren oder vorübergehend löschen – die Anwendung legt beim nächsten Start eine neue Datei mit Standardwerten an.

## Personio Client Secret

Das Personio Client Secret wird **nicht** in der Klartext-Konfiguration abgelegt, sondern im **Windows Credential Manager** unter dem Eintrag `TimeTracker.Personio` gespeichert.

### Secret hinterlegen

1. Personio API-Credentials einholen (Client ID und Client Secret beim Administrator anfragen).
2. `client_id` und `employee_id` in `config.toml` eintragen.
3. Beim ersten Synchronisations-Versuch fragt die Anwendung nach dem Client Secret und speichert es im Credential Manager.

### Secret prüfen oder ändern

- **Windows-Suche → "Anmeldeinformationsverwaltung"** öffnen.
- Reiter **Windows-Anmeldeinformationen** → Eintrag `TimeTracker.Personio` suchen.
- Eintrag bearbeiten oder löschen. Beim nächsten Sync wird das Secret bei Bedarf erneut abgefragt.

## Speicherorte (kurz)

- **Konfiguration:** `%APPDATA%\TimeTracker\config.toml`
- **Datenbank:** `%LOCALAPPDATA%\TimeTracker\data.db`
- **Logs:** `%LOCALAPPDATA%\TimeTracker\log\`
- **Personio-Secret:** Windows Credential Manager (`TimeTracker.Personio`)

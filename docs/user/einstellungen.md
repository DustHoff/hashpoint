# Einstellungen

Alle Einstellungen lassen sich direkt im **Hauptfenster** im Tab
**Einstellungen** vornehmen. Geänderte Werte werden anschließend in
`%APPDATA%\TimeTracker\config.toml` persistiert. Ein direktes Editieren der
TOML-Datei ist weiterhin möglich, aber für den Normalbetrieb nicht mehr
nötig.

## Aufbau des Tabs

Der Tab ist in drei Abschnitte unterteilt:

1. **Erfassung** — Polling-Intervall und Idle-Schwelle.
2. **Oberfläche** — Autostart-Schalter.
3. **Personio** — Tenant-Subdomain und interaktive Anmeldung.

Am unteren Rand befindet sich der Button **Einstellungen speichern**.
Änderungen treten erst nach dem Speichern in Kraft. Erfolge und
Validierungsfehler werden oben im Tab als Banner angezeigt.

## Erfassung

| Feld | Default | Bereich | Bedeutung |
| --- | --- | --- | --- |
| **Poll-Intervall (Sekunden)** | `2` | `1`–`30` | Wie oft prüft der TimeTracker, welches Fenster im Vordergrund ist. Niedriger = präziser, aber höhere CPU-Last. |
| **Idle-Schwelle (Minuten)** | `5` | `1`–`240` | Nach wie vielen Minuten ohne Tastatur-/Maus-Eingabe der laufende Block beendet und als **Idle** markiert wird. |

## Oberfläche

| Feld | Default | Bedeutung |
| --- | --- | --- |
| **Mit Windows starten (Autostart)** | an | Trägt den TimeTracker als Autostart-Eintrag in die Windows-Registry ein bzw. entfernt ihn. Lässt sich auch über das Tray-Menü umschalten. |

## Personio

| Feld | Bedeutung |
| --- | --- |
| **Tenant (Subdomain)** | Subdomain Ihrer Personio-Instanz. Beispiel: `onesi` → `https://onesi.personio.de`. Erlaubt sind Kleinbuchstaben, Ziffern und Bindestriche. |

Im Personio-Abschnitt sehen Sie zusätzlich den aktuellen Login-Status und
können die Anmeldung anstoßen oder zurücksetzen:

- **Bei Personio anmelden / Erneut anmelden** — startet den interaktiven
  Login-Flow. Dabei öffnet sich ein eigenes Chrome-Fenster auf der
  Personio-Login-Seite. Sobald die Anmeldung abgeschlossen ist, übernimmt
  der TimeTracker die Session-Cookies und schließt den Browser.
- **Session löschen** — entfernt die hinterlegten Cookies. Der Sync ist
  danach erst nach erneutem Login wieder möglich.

> Cookies werden verschlüsselt im **Windows Credential Manager** unter
> `TimeTracker.PersonioSession` abgelegt; die `config.toml` enthält keine
> sensiblen Daten.

Details zum Login-Flow, zu Validierung und Fehlerbehandlung siehe
[Personio-Synchronisation](personio.md).

## `config.toml` — direkter Zugriff (optional)

Wer den Editor lieber direkt verwendet, kann die Datei unter
`%APPDATA%\TimeTracker\config.toml` öffnen. Beispielinhalt:

```toml
[tracking]
poll_interval_sec = 2
idle_threshold_min = 5

[personio]
tenant = "onesi"

[ui]
autostart = true
```

Nach manuellen Änderungen ist ein **Neustart** der Anwendung erforderlich,
damit die neuen Werte greifen. Beim Speichern aus der UI heraus wird die
Datei direkt aktualisiert; Werte werden zudem zur Laufzeit übernommen.

## Speicherorte (kurz)

- **Konfiguration:** `%APPDATA%\TimeTracker\config.toml`
- **Datenbank:** `%LOCALAPPDATA%\TimeTracker\data.db`
- **Logs:** `%LOCALAPPDATA%\TimeTracker\log\`
- **Personio-Session:** Windows Credential Manager
  (`TimeTracker.PersonioSession`)

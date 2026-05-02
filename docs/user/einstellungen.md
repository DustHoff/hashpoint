# Einstellungen

Alle Einstellungen lassen sich direkt im **Hauptfenster** im Tab
**Einstellungen** vornehmen. Geänderte Werte werden anschließend in
`%APPDATA%\TimeTracker\config.toml` persistiert. Ein direktes Editieren der
TOML-Datei ist weiterhin möglich, aber für den Normalbetrieb nicht mehr
nötig.

## Aufbau des Tabs

Der Tab ist in vier Abschnitte unterteilt:

1. **Erfassung** — globaler Erfassungs-Schalter, Polling-Intervall, Idle-Schwelle und Tag-Block-Granularität.
2. **Oberfläche** — Autostart-Schalter.
3. **Quick-Tag-Picker** — globaler Hotkey für die schnelle Tag-Auswahl (siehe [Quick-Tag-Picker](quick-tag.md)).
4. **Personio** — Tenant-Subdomain und interaktive Anmeldung.

Am unteren Rand befindet sich der Button **Einstellungen speichern**.
Änderungen treten erst nach dem Speichern in Kraft. Erfolge und
Validierungsfehler werden oben im Tab als Banner angezeigt.

## Erfassung

| Feld | Default | Bereich | Bedeutung |
| --- | --- | --- | --- |
| **Erfassung der fokussierten Anwendung aktiv** | an | Checkbox | Globaler Schalter für das automatische Fokus-Tracking. Deaktiviert: keine neuen Programm-Blöcke und keine Auto-Tagging-Regeln greifen mehr — manuelles Tagging über das Tray-Submenü bleibt möglich. Der Wert wird persistiert; wirkt sich sofort und auch über Anwendungs-Neustarts hinweg aus. Identisch mit dem Tray-Eintrag „Pause Tracking". |
| **Poll-Intervall (Sekunden)** | `2` | `1`–`300` | Wie oft prüft der TimeTracker, welches Fenster im Vordergrund ist. Niedriger = präziser, aber höhere CPU-Last. |
| **Idle-Schwelle (Minuten)** | `5` | `1`–`240` | Nach wie vielen Minuten ohne Tastatur-/Maus-Eingabe der laufende Block beendet und als **Idle** markiert wird. |
| **Tag-Block-Granularität (Minuten)** | `0` | `0`–`60` | Legt **Tag-Blöcke** (manuelle Range-Tags und Auto-Tag-Blöcke) auf ein **Slot-Raster** dieser Breite (verankert an lokaler Mitternacht, also z. B. `:00/:15/:30/:45` bei `15`). Beginn wird **abgerundet**, Ende **abgerundet**. Auto-Tag-Blöcke unterhalb der Granularität werden nicht erzeugt (Zero-Length-Suppression). **Process-Tracks** sind von dieser Einstellung **nicht** betroffen — der untere Strip zeigt immer die rohen, sekundengenauen Zeiten. Werteänderungen treten ohne Neustart in Kraft (greifen ab dem nächsten Tag-Block-Boundary). `0` deaktiviert das Raster komplett. |

## Oberfläche

| Feld | Default | Bedeutung |
| --- | --- | --- |
| **Mit Windows starten (Autostart)** | an | Trägt den TimeTracker als Autostart-Eintrag in die Windows-Registry ein bzw. entfernt ihn. Lässt sich auch über das Tray-Menü umschalten. |

## Quick-Tag-Picker

| Feld | Default | Bedeutung |
| --- | --- | --- |
| **Globalen Hotkey für den Quick-Tag-Picker registrieren** | an | Aktiviert/deaktiviert die Registrierung eines systemweiten Hotkeys, mit dem aus jedem Fenster heraus eine kompakte Tag-Auswahl unten rechts auf dem Cursor-Monitor geöffnet werden kann. |
| **Hotkey** | `Ctrl+Alt+T` | Tastenkombination im Format `<Mod>+<Mod>+<Taste>`. Modifier: `Ctrl`, `Alt`, `Shift`, `Win`. Tasten: `A`–`Z`, `0`–`9`, `F1`–`F24`. Mindestens ein Modifier ist Pflicht. **Hinweis:** `Win+T` ist von Windows belegt (Taskleisten-Fokus) und sollte vermieden werden. |

Funktionsweise und Bedienung sind detailliert unter [Quick-Tag-Picker](quick-tag.md) beschrieben.

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
enabled                    = true   # globaler Schalter für Fokus-Tracking + Auto-Tagging
poll_interval_sec          = 2
idle_threshold_min         = 5
tag_block_granularity_min  = 0      # 0 = aus; 15 = Tag-Blöcke (manuell+auto) auf
                                    # 15-min-Slots snappen. Process-Tracks bleiben roh.

[personio]
tenant = "onesi"

[ui]
autostart = true

[quick_tag]
enabled = true
hotkey  = "Ctrl+Alt+T"
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

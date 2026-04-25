# Personio-Synchronisation

Der TimeTracker kann erfasste und getaggte Blöcke als **Anwesenheitsbuchungen** an Personio übertragen. Die Synchronisation wird ausschließlich **manuell** angestoßen – es gibt keinen Auto-Sync.

## Voraussetzungen

1. Personio API-Zugangsdaten (vom Administrator):
   - **Client ID**
   - **Client Secret**
   - **Eigene Mitarbeiter-ID (Employee ID)**
2. In `%APPDATA%\TimeTracker\config.toml` eintragen:
   ```toml
   [personio]
   client_id = "<Client ID>"
   employee_id = "<Mitarbeiter-ID>"
   base_url = "https://api.personio.de/v1"
   ```
3. Beim ersten Sync wird das **Client Secret** abgefragt und im Windows Credential Manager als `TimeTracker.Personio` hinterlegt.
4. Mindestens ein **Tag mit Personio Project ID** muss existieren (siehe [Tags verwalten](tags.md)).

## Sync auslösen

### Tag synchronisieren

1. Tab **Zeitachse** öffnen.
2. Den gewünschten Tag wählen (Datum-Eingabe, Vortag/Folgetag).
3. Auf **Sync zu Personio** klicken.
4. Während der Übertragung steht der Button auf *Synchronisiere…*.
5. Nach Abschluss erscheint eine Ergebnis-Zeile:
   `Periode(n): X, Blöcke verarbeitet: Y, Blöcke übersprungen: Z`

### Schnell-Sync via Tray

Im Tray-Menü gibt es den Punkt **Sync zu Personio (heute)**. Damit wird der heutige Tag synchronisiert, ohne dass das Hauptfenster geöffnet werden muss. Fehler erscheinen nur im Log.

## Was wird übertragen?

Der TimeTracker fasst Blöcke pro Tag und Personio-Mapping zu **Perioden** zusammen:

- Alle Blöcke mit identischer (Datum, Project ID, Activity ID) werden zu **einer** Personio-Anwesenheit zusammengefasst.
- **Startzeit:** früheste Blockstart-Zeit der Gruppe (in lokaler Zeitzone).
- **Endzeit:** späteste Blockend-Zeit der Gruppe (in lokaler Zeitzone).
- **Datum:** lokaler Tag.
- **Kommentar:** automatisch aus Tag-Namen und -Beschreibungen erzeugt.
- **Project ID / Activity ID:** aus dem Tag-Mapping (mit Vererbung Sub-Tag → Eltern-Tag).

Nach erfolgreicher Übertragung werden die zugehörigen Blöcke in der lokalen Datenbank mit `synced_at` und der Personio-Record-ID markiert.

## Welche Blöcke werden synchronisiert?

Ein Block wird übertragen, wenn **alle** folgenden Bedingungen erfüllt sind:

- ✅ Block ist nicht als **Idle** markiert
- ✅ Block hat einen **Tag**
- ✅ Block hat **Start- und Endzeit** (kein offener Block)
- ✅ Tag hat **Zu Personio synchronisieren = an**
- ✅ Tag hat (ggf. via Vererbung) eine **nicht-leere Project-ID**

Andernfalls wird er in der Statistik unter **„übersprungen"** gezählt.

## Fehler & Wiederholungen

### Automatische Wiederholung

Der HTTP-Client wiederholt fehlgeschlagene Requests automatisch:

- Bis zu **3 Versuche** mit exponentiellem Backoff
- Wiederholt bei `5xx` (Serverfehler) und `429` (Rate-Limit)
- Bei `401` (nicht autorisiert) wird das OAuth-Token erneuert und der Request einmal wiederholt
- Rate-Limit-Header (`X-RateLimit-*`) werden beachtet

### Fehleranzeige

Schlägt der Sync trotz Wiederholungen fehl, erscheint im Tab **Zeitachse** ein rotes Banner mit der Fehlermeldung. Häufige Ursachen:

| Fehler | Ursache & Lösung |
| --- | --- |
| `unauthorized` / `401` | Client ID, Secret oder Employee ID falsch. In `config.toml` und Credential Manager prüfen. |
| `connection refused` / Timeout | Keine Internet-Verbindung oder VPN benötigt. |
| `forbidden` / `403` | API-Rechte fehlen. Personio-Administrator kontaktieren. |
| `unprocessable entity` / `422` | Ungültige Project-/Activity-Kombination. Tag-Mappings prüfen. |
| `Blöcke übersprungen: N` (alle) | Keine Blöcke erfüllen die Sync-Bedingungen. Tags und Idle-Status prüfen. |

## Erneut synchronisieren

Wird ein bereits synchronisierter Tag erneut synchronisiert, prüft der Client per Personio-Record-ID, welche Blöcke schon übertragen wurden. Doppelte Buchungen werden so vermieden. Korrekturen (z. B. Tag-Änderung an einem bereits synchronisierten Block) werden aktuell **nicht** automatisch nach Personio gespiegelt – in solchen Fällen die Buchung in Personio manuell anpassen.

## Datenschutz

- Authentifizierungs-Header werden **niemals** geloggt.
- Logs enthalten keine Fenstertitel oberhalb von Debug-Level.
- Das Client Secret liegt verschlüsselt im Windows Credential Manager und wird nicht im Klartext gespeichert.

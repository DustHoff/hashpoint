# Personio-Synchronisation

Der TimeTracker kann erfasste und getaggte Blöcke als **Anwesenheitsbuchungen**
direkt in Personio anlegen — und zwar über dieselbe interne API, die auch
die Personio-Web-Oberfläche nutzt. Dafür brauchen wir keine
Admin-/API-Tokens auf Unternehmensebene; der TimeTracker meldet sich mit
**Ihrem** persönlichen Personio-Login an.

## Wie die Anmeldung funktioniert

Die Personio-Login-Seite kann auf MFA, SSO etc. zurückgreifen — wir starten
deshalb einen **eigenen Chrome-Browser**, in dem Sie ganz normal interaktiv
einloggen. Der TimeTracker hört per
[Chrome DevTools Protocol](https://chromedevtools.github.io/devtools-protocol/)
mit, was passiert, übernimmt die nach dem Login gesetzten **Session-Cookies**
und schließt das Fenster wieder. Anschließend werden die Cookies verschlüsselt
im Windows Credential Manager hinterlegt.

> Es werden **keine** Anmeldedaten (Benutzername/Passwort/MFA-Code) an den
> TimeTracker übertragen. Er sieht nur die nach erfolgreicher Anmeldung
> gesetzten Cookies und den CSRF-Token.

## Voraussetzungen

- **Google Chrome** muss installiert sein (das System-Chrome, nicht die
  Wails-WebView). Edge wird nicht unterstützt.
- Sie müssen die Personio-**Tenant-Subdomain** kennen
  (`https://<tenant>.personio.de`) — meist der Firmenkurzname.

## Personio konfigurieren

1. Im Hauptfenster auf den Tab **Einstellungen** wechseln.
2. Im Bereich **Personio** den Tenant eintragen (z. B. `onesi`).
3. Auf **Einstellungen speichern** klicken.

## Anmelden

1. Auf **Bei Personio anmelden** klicken.
2. Es öffnet sich ein neues Chrome-Fenster auf
   `https://<tenant>.personio.de/login/index`. Loggen Sie sich dort wie
   gewohnt ein (E-Mail, Passwort, ggf. MFA, ggf. SSO).
3. Sobald Personio Sie auf das Dashboard weiterleitet, übernimmt der
   TimeTracker automatisch die Session und schließt das Browserfenster.
4. Im Anschluss validiert der TimeTracker die Session, indem er einen
   anonymen Aufruf gegen die Personio-App macht. Werden Sie auf `/login`
   zurückgeleitet, schlägt die Validierung fehl — sonst gilt die Session als
   gültig.
5. Nach erfolgreicher Validierung wird die Mitarbeiter-ID einmalig über
   `/api/v1/navigation/context` ermittelt und mit der Session gespeichert.

Status und Zeitstempel der erfassten Sitzung sehen Sie jederzeit unten im
Personio-Bereich der Einstellungen.

## Erneut anmelden / abmelden

- **Erneut anmelden:** öffnet wieder das Chrome-Fenster und überschreibt die
  bestehende Session. Notwendig, wenn Personio die Sitzung beendet hat (z. B.
  nach längerer Inaktivität oder Passwortwechsel).
- **Session löschen:** entfernt die im Credential Manager hinterlegten
  Cookies. Bis zur nächsten Anmeldung sind keine Synchronisationen möglich.

## Synchronisation auslösen

Der eigentliche Sync funktioniert wie zuvor:

1. Tab **Zeitachse** öffnen, Datum wählen.
2. Auf **Sync zu Personio** klicken (oder im Tray-Menü
   **Sync zu Personio (heute)**).
3. Der TimeTracker holt zunächst das **Timesheet** für den/die gewählten
   Tag(e) (`GET /svc/attendance-bff/v1/timesheet/{employee_id}`), gruppiert
   die getaggten Blöcke pro Tag und Personio-Projekt-ID, und schreibt pro Tag
   ein `PUT /svc/attendance-api/v1/days/{day_id}?autoFix=true&usedInTimesheet=true`
   mit den Perioden. Hat ein Tag noch keinen Personio-Datensatz, generiert
   der Client eine UUID und legt den Tag damit an (Upsert).
4. Die Antwort wird im Banner unter der Zeitachse angezeigt:
   `Periode(n): X, Blöcke verarbeitet: Y, Blöcke übersprungen: Z`.

## Was wird übertragen?

Pro Tag bündelt der TimeTracker alle nicht-idlen, getaggten Blöcke nach
**Personio-Projekt-ID** (Pflichtfeld) und Kommentar. Daraus entsteht je
Bucket eine Periode mit:

| Feld | Quelle |
| --- | --- |
| `period_type` | immer `"work"` |
| `start` / `end` | früheste Block-Startzeit / späteste Block-Endzeit, formatiert als lokal-naive ISO-8601 (`YYYY-MM-DDTHH:MM:SS`, ohne Zeitzone) |
| `project_id` | Personio-Projekt-ID des Tags (mit Vererbung Sub-Tag → Eltern-Tag) — `null` falls nicht gesetzt |
| `comment` | aus Tag-Namen + ggf. Tag-Beschreibung erzeugt; je Block-Beschreibung mit ` — ` angehängt |
| `auto_generated` | immer `false` |

> Personios UI-API kennt im Period-Modell **keine Activity-ID**. Das alte
> Feld `personio_activity_id` an Tags bleibt als Legacy-Feld bestehen und
> wird derzeit beim Sync **nicht** verwendet — Personio kann das in einer
> späteren UI-Version wieder einführen.

## Welche Blöcke werden synchronisiert?

Ein Block wird übertragen, wenn alle folgenden Bedingungen gelten:

- ✅ kein Idle-Block
- ✅ Block hat einen **Tag**
- ✅ Block hat **Start- und Endzeit**
- ✅ Tag hat **Zu Personio synchronisieren = an**
- ✅ Tag hat (ggf. via Vererbung) eine **nicht-leere Project-ID**

Andere Blöcke landen in der **„Übersprungen"-Statistik**.

## Fehlerbehandlung

| Fehlermeldung | Ursache | Lösung |
| --- | --- | --- |
| *„session expired — please re-authenticate"* | Personio hat die Cookies invalidiert. | In den Einstellungen **Erneut anmelden** klicken. |
| *„kein Timesheet-Eintrag — Personio betrachtet diesen Tag als nicht buchbar"* | Personio liefert für diesen Tag keinen Eintrag. | Datum prüfen, ggf. liegt Personios Mitarbeiterdatum außerhalb. |
| *„Tag ist in Personio … und kann nicht beschrieben werden"* | Tag ist `non_trackable` / `locked` (Wochenende, Feiertag, gesperrter Zeitraum). | In Personio prüfen, ggf. Sperre durch HR aufheben lassen. |
| *„fetch employee id"* | `/api/v1/navigation/context` antwortet nicht erwartungsgemäß. | Erneut anmelden. |
| *„kein Personio-Tenant in den Einstellungen hinterlegt"* | Tenant in den Einstellungen leer. | Tenant eintragen, speichern. |

Authentifizierungs-Header werden niemals geloggt; Cookies liegen
verschlüsselt im Windows Credential Manager (`TimeTracker.PersonioSession`).

## Manuell prüfen

Wer das Cookie selber inspizieren möchte:

1. **Windows-Suche → „Anmeldeinformationsverwaltung"**.
2. Reiter **Windows-Anmeldeinformationen** → Eintrag
   `TimeTracker.PersonioSession`.
3. Inhalt ist ein JSON-Blob (`tenant`, `employee_id`, `cookies[]`,
   `captured_at`).

## Datenschutz

- Es wird **nur** mit der konfigurierten Personio-Subdomain kommuniziert.
- Auth-Header (`X-CSRF-Token`, Cookie) erscheinen niemals im Log.
- Beim Klick auf **Session löschen** wird der Credential-Manager-Eintrag
  vollständig entfernt.

# ADR 0010 – Personio-Anbindung über die interne UI-API mit CDP-Login

- **Status:** Akzeptiert (retrospektiv dokumentiert)
- **Datum:** 2026-09-28
- **Bezug:** Spec §2.5.1, §2.5.2; CLAUDE.md §8; `internal/personio`; `docs/technical/{personio-api,cdp-login}.md`

## Kontext

Personios öffentliche Attendance-API erlaubt nur **firmenweite** OAuth-Credentials. Hashpoint ist ein Endanwender-Werkzeug; firmenweite Secrets auf Client-Geräten sind inakzeptabel. Mitarbeiter sollen nur ihre eigenen Zeiten schreiben.

## Entscheidung

- Nutzung der **internen UI-API** (dieselbe wie die Personio-Web-App): `GET /api/v1/navigation/context`, `GET /svc/attendance-bff/v1/timesheet/{employee_id}`, `PUT /svc/attendance-api/v1/days/{day_id}`.
- **Login über eine eigene Chrome-Instanz via `chromedp`/CDP**: Anwender meldet sich interaktiv an (inkl. MFA/SSO), Cookies werden übernommen, der tatsächliche `AppHost` wird erfasst.
- Auth ist **cookie-basiert**; das XSRF-Cookie wird als `x-athena-xsrf-token` gespiegelt. Keine automatische Redirect-Verfolgung: `401/403` oder `30x → /login` ⇒ `ErrSessionExpired`.
- Session-Blob wird im **Windows Credential Manager** (`TimeTracker.PersonioSession`) gespeichert, nie in `config.toml`; Auth-Header werden nie geloggt.
- **Status-Badge** im Programmkopf prüft die Session alle 60 s; Klick startet den Login.
- Plugins erhalten die Session über `HostAPI.RequestPersonioSession`; Re-Auth wird serialisiert (max. ein Chrome-Fenster).

## Konsequenzen

- ✅ Keine firmenweiten Secrets; Rechte = Rechte des Anwenders.
- ✅ SSO/MFA funktionieren ohne Sonderlogik.
- ⚠️ **Undokumentierte API** – Änderungen seitens Personio können den Sync brechen; Endpunkte sind per HAR-Capture verifiziert und in Tests gemockt.
- ⚠️ Installiertes Chrome ist Voraussetzung.

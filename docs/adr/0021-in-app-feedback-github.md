# ADR 0021 – In-App-Feedback als GitHub-Issue über Device-Flow mit sanitisiertem Log

- **Status:** Akzeptiert (retrospektiv dokumentiert)
- **Datum:** 2026-09-28
- **Bezug:** `internal/feedback`, `Feedback.tsx`; `docs/user/feedback.md`, `docs/technical/github-api.md`

## Kontext

Fehlerberichte kamen bislang ohne Logs und Reproduktionsschritte an. Gleichzeitig verbietet die Datenschutzlinie Telemetrie und das Hochladen von Fenstertiteln.

## Entscheidung

- Tab **Feedback** erstellt Issues direkt im Repository `DustHoff/hashpoint`.
- Auth über eine **GitHub App mit Device-Flow** (öffentliche Client-ID im Binary, kein Passwort in Hashpoint); User-to-Server-Token + Refresh im **Credential Manager** (`TimeTracker.GitHubFeedback`), getrennt von Personio.
- Strukturiertes Formular (Titel, Kategorie, Schweregrad, Beschreibung, Erwartet/Tatsächlich, Schritte); Labels aus Kategorie/Schweregrad (best effort).
- Optionaler **Log-Anhang** (Heute / 1 h / 24 h) inkl. erkannter **Abstürze** (aus crashguard-Einträgen, max. begrenzt); **Debug-Einträge und Fenstertitel werden vor dem Upload entfernt**.
- **Pflicht-Vorschau** des vollständigen Markdown vor dem Absenden.
- Kein Backend-Proxy; ausschließlich Kommunikation mit github.com und nur auf explizite Aktion.

## Konsequenzen

- ✅ Reproduzierbare Bug-Reports mit Kontext, ohne Telemetrie.
- ✅ Anwender sieht genau, was hochgeladen wird.
- ⚠️ Issues sind öffentlich; Sanitisierung muss bei neuen Log-Feldern mitgepflegt werden.
- ⚠️ GitHub-Konto erforderlich.

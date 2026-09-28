# ADR 0023 – Datenschutz- und Sicherheits-Baseline (Logging, Secrets, CSP)

- **Status:** Akzeptiert (retrospektiv dokumentiert)
- **Datum:** 2026-09-28
- **Bezug:** CLAUDE.md §5, §6, §12; Spec §3; `internal/logging`, `cmd/timetracker/csp.go`

## Kontext

Fenstertitel können vertrauliche Inhalte enthalten (Mail-Betreffe, Dokumentnamen, URLs). Hashpoint hält zudem mehrere Sessions (Personio, Entra, GitHub) und rendert Inhalte in einem privilegierten WebView.

## Entscheidung

- **Keine Telemetrie**; externe Verbindungen nur zu explizit genutzten Diensten (Personio, Microsoft, GitHub-Feedback, Plugins).
- **Logging** mit `log/slog` (JSON in Produktion), rotierende Datei unter `%LOCALAPPDATA%\TimeTracker\log\`; **Fenstertitel/PII nie auf Info+**, Auth-Header/Cookies/Tokens nie.
- **Secrets** nie in Config, Logs, Errors oder Frontend-Bindings: Credential Manager (Personio, GitHub), DPAPI (Entra-Cache, Plugin-Passwörter).
- **Strikte Content-Security-Policy** für die eingebettete UI (`script-src 'self'`, kein `object`, `frame-ancestors 'none'` …) als Defense-in-Depth.
- User-Regex nur via RE2; Pfad-Parameter (Hilfe-Slugs, Plugin-Namen) per Whitelist/Validierung.
- Collector↔UI nur über **per-User-Named-Pipe** (keine TCP-Ports, ADR 0001).

## Konsequenzen

- ✅ Datenschutzkonform ohne Zusatzkonfiguration; Logs sind teilbar (Feedback).
- ⚠️ Debug-Logs enthalten Titel und dürfen nicht ungefiltert weitergegeben werden.

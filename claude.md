# CLAUDE.md – Coding Guidelines TimeTracker

Dieses Dokument legt verbindliche Konventionen für die Entwicklung des TimeTracker-Tools fest. Bei Konflikten gilt: diese Datei > Spec > eigenes Urteil.

---

## 1. Projektkontext

- **Sprache:** Go 1.22+
- **UI:** Wails v2 (Go-Backend, TypeScript + React + Vite + Tailwind CSS)
- **DB:** SQLite via `modernc.org/sqlite` (pure Go, kein CGO)
- **Plattform:** Windows 10/11, x64
- **Build:** `wails build` für Release; `go build -ldflags="-H windowsgui"` für Backend-only Tests
- Vor jeder Änderung: zugehörigen Abschnitt der Spec (`zeiterfassung-spec.md`) lesen.

---

## 2. Projektstruktur (verbindlich)

```
/cmd/timetracker          main, Tray-Setup, Wails-Bootstrap
/internal/tracker         Fokus-Polling, Idle-Detection
/internal/winapi          Windows-API-Wrapper
/internal/storage         SQLite-Repos, Migrationen
/internal/tagging         Tag-Hierarchie, Auto-Tag-Engine
/internal/personio        Personio-API-Client
/internal/config          Settings (TOML)
/internal/logging         slog-Setup
/internal/app             Wails-Bindings (App-Struct mit JS-exponierten Methoden)
/frontend                 Wails-Frontend (TS/React)
/migrations               SQL-Migrations (.sql, sequenziell nummeriert)
/test                     Integrationstests
```

- Code in `/internal` ist **nicht** außerhalb des Moduls importierbar — bewusst so.
- Keine zirkulären Imports. `tracker` darf nicht von `app` oder `personio` importieren.
- Pro Package eine klare Verantwortung; keine "utils"- oder "common"-Pakete.

---

## 3. Go-Konventionen

### 3.1 Stil
- `gofmt` + `goimports` sind Pflicht. CI lehnt unformatierten Code ab.
- `golangci-lint` mit aktiviert: `errcheck`, `govet`, `staticcheck`, `revive`, `gosec`, `gocritic`, `errorlint`.
- Exportierte Symbole brauchen Godoc-Kommentare im Stil `// FuncName does ...`.
- Keine globalen Variablen außer Konstanten und Logger-Fallback.

### 3.2 Naming
- Packages: kurz, lowercase, kein Underscore (`tagging`, nicht `tag_engine`).
- Interfaces enden auf `-er` wenn sinnvoll (`Repository`, `Tracker`), sonst aussagekräftig.
- Receiver-Namen: 1–2 Buchstaben, konsistent pro Typ (`r *FocusRepo`, nicht mal `r` mal `repo`).

### 3.3 Fehlerbehandlung
- **Niemals `_ = err` oder stilles Verschlucken.** Errors werden behandelt, geloggt oder weitergereicht.
- Wrappen mit `fmt.Errorf("context: %w", err)`. Sentinel-Errors als `var ErrXxx = errors.New(...)` im Package-Root.
- Auf typed errors mit `errors.Is` / `errors.As` prüfen, nie auf String-Vergleich.
- `panic` nur bei Programmierfehlern (nil-Pointer in Konstruktor o. Ä.), niemals als Control-Flow.

### 3.4 Concurrency
- Jede Goroutine braucht einen klaren Stop-Mechanismus (`context.Context` oder Done-Channel).
- Kein `time.Sleep` als Sync — `time.Ticker` oder `<-ctx.Done()`.
- Geteilten State über Channels oder explizite `sync.Mutex` schützen. Mutex direkt neben dem geschützten Feld deklarieren.
- Tracking-Loop läuft in **einer** dedizierten Goroutine, nicht aus mehreren Stellen gestartet.

### 3.5 Kontext
- Alle I/O-Funktionen (DB, HTTP, Windows-API soweit sinnvoll) nehmen `ctx context.Context` als ersten Parameter.
- Kein `context.Background()` außerhalb von `main` und Tests.

---

## 4. Datenbank

- **Migrations only, nie Schema-Änderungen im Code.** Tool: `golang-migrate` oder `pressly/goose`. Dateien in `/migrations` als `0001_init.up.sql` / `0001_init.down.sql`.
- Repository-Pattern: jede Tabelle hat ein `XxxRepo`-Struct mit explizit definiertem Interface in `storage/interfaces.go`.
- Queries als Konstanten am Datei-Anfang oder via `sqlc` generiert. Keine String-Concatenation für SQL.
- **Prepared Statements** für wiederholte Queries (Tracking-Loop).
- Transaktionen für jede Operation, die > 1 Tabelle berührt.
- Zeitstempel immer in **UTC** speichern, erst im UI in lokale Zeit konvertieren.
- DB-Pfad: `%LOCALAPPDATA%\TimeTracker\data.db` — nie hardcoden, immer aus `config`.

---

## 5. Logging

- `log/slog` aus der Standardbibliothek mit JSON-Handler in Produktion, Text-Handler in Dev.
- Log-Levels: `Debug` (Polling-Details), `Info` (Lifecycle, Sync-Erfolg), `Warn` (Recoverable), `Error` (echte Fehler).
- **Keine PII oder Fenstertitel auf `Info`-Level loggen** — Titel können sensible Informationen enthalten. Auf `Debug` ok.
- Strukturierte Felder, nicht in die Message: `slog.Info("block closed", "duration_sec", d, "process", p)`.
- Kein `fmt.Println` außer in `main` für Startup-Banner.

---

## 6. Konfiguration

- TOML in `%APPDATA%\TimeTracker\config.toml`.
- Secrets (Personio Client Secret) **niemals** in Logs, Errors oder Frontend-Bindings ausgeben.
- Default-Werte zentral in `config/defaults.go`.
- Validierung beim Laden — Programm bricht mit klarer Fehlermeldung ab statt mit korrupter Config zu starten.

---

## 7. Windows-API

- Alle Win32-Calls hinter `internal/winapi` kapseln. Kein direkter `syscall`/`windows.NewLazyDLL` außerhalb.
- Funktionen typisiert wrappen: `GetForegroundWindow() (HWND, error)`, nicht rohe Pointer durchreichen.
- UTF-16 ↔ UTF-8 Konvertierung mit `golang.org/x/sys/windows.UTF16PtrFromString` / `.UTF16ToString`.
- Tests für `winapi` nur als Build-Tag `//go:build windows` markieren.

---

## 8. Personio-Client

- HTTP-Client mit Timeout (10s default), kein `http.DefaultClient`.
- Retry mit exponentiellem Backoff für 5xx und 429, max. 3 Versuche.
- Rate-Limit-Header (`X-RateLimit-*`) auswerten und respektieren.
- Token-Refresh transparent im Client, nicht im Caller.
- Request/Response-Bodies bei Fehlern auf `Debug`-Level loggen, **nie** Auth-Header.
- Mock-Server für Tests via `httptest.Server`, kein Hit gegen echte Personio-API in CI.

---

## 9. Frontend (Wails)

- TypeScript **strict mode**. `any` ist verboten außer mit Kommentar warum.
- Komponenten als Function-Components mit Hooks. Keine Class-Components.
- State: lokal mit `useState`/`useReducer`, global mit Zustand oder React Context — kein Redux.
- Wails-Bindings in einem zentralen `src/api/` Layer kapseln, Komponenten rufen nie `window.go.*` direkt.
- Tailwind-Klassen direkt im JSX, kein CSS-in-JS. Wiederkehrende Pattern als Component, nicht als `@apply`.
- Date/Time-Handling: **immer** über eine Lib (`date-fns` oder `dayjs`), nie `Date`-Mathematik von Hand.
- Timeline-Komponente: `react-calendar-timeline` oder `vis-timeline` — Entscheidung in Phase 3 dokumentieren.

---

## 10. Tests

- **Unit-Tests Pflicht** für: `tracker` (Block-Logik, Idle-Detection), `tagging` (Regel-Engine), `personio` (Comment-Aufbau, Aggregation), `storage` (Repos via In-Memory-SQLite).
- Test-Dateien `*_test.go` neben dem Code, Package `xxx_test` für Black-Box-Tests öffentlicher API.
- Table-driven Tests sind der Default. Sub-Tests via `t.Run(name, ...)`.
- Coverage-Ziel: `internal/tagging` und `internal/personio` ≥ 80 %, Rest ≥ 60 %.
- Integrationstests in `/test` mit Build-Tag `//go:build integration`.
- Keine Tests gegen echte externe Services (Personio) in CI — Fixtures + httptest.

---

## 11. Git & Commits

- **Conventional Commits**: `feat:`, `fix:`, `refactor:`, `test:`, `docs:`, `chore:`.
- Commit-Body erklärt das *Warum*, nicht das *Was*.
- Ein Commit = eine logische Änderung. Keine "WIP" oder "fixes" als finale Commits.
- Branches: `feat/auto-tagging`, `fix/idle-detection`. Kein direkter Push auf `main`.
- PRs brauchen grünes CI: `gofmt`, `golangci-lint`, `go test ./...`, Frontend-Build.

---

## 12. Sicherheit

- Personio-Credentials im Windows Credential Manager via `github.com/danieljoos/wincred`, **nicht** im Klartext in der Config.
- Keine Logs der Fenstertitel auf `Info`+ Level (s. §5).
- DB-Datei mit User-only Permissions anlegen (Standard von `LOCALAPPDATA`, aber explizit prüfen).
- Regex aus User-Input (Auto-Tag-Regeln) ausschließlich mit Go-`regexp` (RE2) compilen — kein Drittlib mit Backtracking.
- Keine Telemetrie, keine externen Calls außer Personio.

---

## 13. Was Claude Code NICHT tun soll

- Keine neuen Dependencies hinzufügen, ohne sie kurz zu begründen (Größe, Maintenance-Status).
- Kein CGO einführen — Build muss pure Go bleiben.
- Keine `init()`-Funktionen für Logik, nur für Driver-Registration o. Ä.
- Keine breaking Schema-Changes ohne Up- *und* Down-Migration.
- Keine UI-Strings hardcoden, wenn Internationalisierung absehbar ist — i18n-Stub vorsehen (auch wenn aktuell nur DE).
- Spec und CLAUDE.md nicht ohne expliziten Auftrag des Users ändern.

---

## 14. Definition of Done (pro Feature)

1. Code formatiert, gelinted, kompiliert.
2. Unit-Tests vorhanden und grün.
3. Godoc auf exportierten Symbolen.
4. Migration vorhanden falls DB-relevant.
5. Manueller Smoke-Test unter Windows durchgeführt und im PR notiert.
6. Spec-Abschnitt und/oder CLAUDE.md aktualisiert wenn sich Verhalten/Konventionen geändert haben.
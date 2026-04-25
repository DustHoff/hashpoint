# Zeiterfassungstool – Spezifikation für Claude Code

## 1. Projektübersicht

Entwicklung eines Windows-Zeiterfassungstools in **Go**, das automatisch erfasst, welche Anwendung gerade den Fokus hat. Erfasste Zeitblöcke können nachträglich getaggt und als Zeiterfassung nach **Personio** synchronisiert werden.

**Plattform:** Windows 10/11 (64-bit)
**Sprache:** Go (Version 1.22+)
**Speicherort:** Lokale SQLite-Datenbank
**Bedienung:** Tray-Icon + On-Demand-Fenster

---

## 2. Funktionale Anforderungen

### 2.1 Fokus-Tracking (Hintergrund)
- Polling alle **1–2 Sekunden** des aktuell fokussierten Fensters via Windows API (`GetForegroundWindow`, `GetWindowThreadProcessId`, `GetWindowTextW`).
- Erfasst werden:
  - Prozessname (z. B. `chrome.exe`)
  - Fenstertitel (enthält oft Dateiname/URL/Tab)
  - Startzeitpunkt des Fokus
  - Endzeitpunkt des Fokus (= Beginn des nächsten Fokuswechsels)
- Konsekutive identische Fokussierungen (gleicher Prozess + gleicher Titel) werden zu einem Block zusammengefasst.
- **Idle-Detection:** Wenn der User > 5 Min inaktiv ist (kein Input via `GetLastInputInfo`), wird der aktuelle Block beendet und als „Idle" markiert.
- **Lock/Sleep:** Beim Sperren oder Ruhezustand wird der aktuelle Block sauber abgeschlossen.

### 2.2 Tray-Icon
- Tool startet minimiert in der Windows-Taskleiste (System Tray).
- **Linksklick** auf Icon → öffnet Hauptfenster mit Zeitachse.
- **Rechtsklick** → Kontextmenü mit:
  - Öffnen
  - Pause / Resume Tracking
  - Sync zu Personio
  - Einstellungen
  - Beenden
- **Autostart**-Option (Registry-Eintrag unter `HKCU\...\Run`).

### 2.3 Hauptfenster (Zeitachse)
- Tagesansicht zweigeteilt: **horizontaler Zeitstrahl oben** (24-Stunden-Strip),
  **Tabellenliste aller Programme/Erfassungen darunter**.
- Datums-Navigation (Vor/Zurück, Datepicker).
- Tabellenliste je Block mit:
  - Programm-Icon/-Name
  - Fenstertitel
  - Start–Ende, Dauer
  - Tätigkeitsbeschreibung (siehe 2.3.1)
  - zugewiesener Tag (mit Auto-Tag-Indikator ⚙)
- **Zeitstrahl-Strip** rendert den Tag von 00:00 bis 24:00 als horizontalen
  Balken. Zusammenhängende Blöcke mit demselben Tag werden zu **einem
  Tag-Segment** zusammengefasst und in der Tag-Farbe dargestellt; Idle-Blöcke
  als blasser Streifen, ungetaggte Blöcke als grauer Streifen.
- **Range-Selektion auf dem Strip:** Per Drag (linke Maustaste) wird eine
  Zeitspanne aufgezogen; alle nicht-idlen Blöcke, die diesen Bereich schneiden,
  werden in einem Schwung selektiert. `Shift+Drag` erweitert die bestehende
  Auswahl additiv.
- **Klick auf ein Tag-Segment** wählt alle Blöcke des Segments aus
  (`Shift+Klick` = additiv).
- **Hover-Highlight:** Mouse-Over auf einem Tag-Segment hebt die zugehörigen
  Programmzeilen in der Tabelle hervor (und umgekehrt soll auf Hover über eine
  Zeile das Segment im Strip betont werden).
- **Multi-Select in der Tabelle:** Klick toggelt, `Shift+Klick` wählt einen
  zusammenhängenden Bereich.
- **Tag- & Beschreibungs-Zuweisung** an die Auswahl: Tag-Buttons + freies
  Textfeld (siehe 2.3.1). Auswahl löschen ist explizit möglich.
- Aggregierte Ansicht: Summen pro Tag.
- Manuelles Editieren (Block teilen, Zeit anpassen, löschen).

### 2.3.1 Tätigkeitsbeschreibung pro Block
- Jeder Fokus-Block hat ein optionales freies Textfeld `description`.
- Über die Selektion auf dem Strip oder in der Tabelle lassen sich beliebig
  viele Blöcke auf einmal mit demselben Tag **und** derselben Beschreibung
  versehen — typische Anwendung: ein zusammenhängender Tag-Block, in dem
  mehrere Programme involviert waren (IDE + Browser + Terminal), bekommt
  einen einzelnen Beschreibungstext (z. B. „Refactoring Login-Flow").
- Beim Personio-Sync wird die Block-Beschreibung an den aus Tag/Sub-Tag
  generierten Kommentar angehängt (Format: `"<tag-comment> — <description>"`);
  Beschreibungen werden je Aggregations-Bucket dedupliziert.

### 2.4 Tags (mit Hierarchie)
- Frei definierbare Tags in **zwei Ebenen**: Parent-Tag (z. B. `#projekta`) und Sub-Tag (z. B. `#frontend`, `#meeting`, `#review`).
- **Namensschema:** `#` + alphanumerische Folge (`^#[a-zA-Z0-9]+$`). Eingabe ohne `#` wird automatisch ergänzt; Validierung lehnt Sonderzeichen/Leerzeichen ab.
- Sub-Tags können eine **freitextliche Beschreibung** (`description`) haben (z. B. „Refactoring Login-Flow").
- Sub-Tags erben standardmäßig das Personio-Mapping des Parents, können es aber überschreiben.
- Pro Tag konfigurierbar:
  - `name` (Hashtag-Format), Farbe
  - `description` (nur Sub-Tags, optional)
  - `parent_id` (NULL = Top-Level)
  - Personio-Mapping: `personio_project_id`, `personio_activity_id`
  - Flag: zu Personio synchronisieren ja/nein
- Ein Block wird genau **einem** Tag zugeordnet — das kann ein Parent oder ein Sub-Tag sein.
- UI: kaskadierender Selector (Parent → Sub) bei Block-Zuweisung; Sub-Tags ohne Mapping fallen auf das Parent-Mapping zurück.

### 2.4.1 Auto-Tagging (Ausbaustufe 2)
- Regelbasierte automatische Tag-Zuweisung beim Erfassen eines neuen Blocks.
- Eine Regel besteht aus:
  - `match_field`: `process_name` | `window_title` | `both`
  - `match_type`: `contains` | `equals` | `regex`
  - `pattern`: Suchstring/Regex
  - `tag_id`: Ziel-Tag (Parent oder Sub)
  - `priority`: Integer; höhere Priorität gewinnt bei Mehrfach-Match
  - `enabled`: bool
- Auswertung: nach jedem Block-Abschluss werden Regeln in Reihenfolge der Priorität geprüft; erste passende Regel setzt `tag_id`.
- **Regex-Engine:** Go-Standardbibliothek `regexp` (RE2-Syntax) — kein Backtracking, lineare Laufzeit, keine Catastrophic-Regex-Risiken. Ungültige Patterns werden beim Speichern via `regexp.Compile` validiert und abgelehnt.
- Manuell gesetzte Tags werden **nicht überschrieben** (Flag `auto_tagged` in `focus_blocks`).
- UI: eigener Bereich „Auto-Tagging-Regeln" mit Test-Funktion gegen vorhandene Blöcke.
- Bulk-Apply: Regel rückwirkend auf bereits erfasste, ungetaggte Blöcke anwenden.

### 2.5 Personio-Synchronisation
- Aggregation aller getaggten Blöcke eines Tages **pro effektivem Mapping**.
- API-Aufruf an Personio Attendance API (`POST /v1/company/attendances`).
- Auth via OAuth Client Credentials (Client ID / Secret aus Settings).
- **Beschreibungs-Format** (`comment`-Feld in Personio): zusammengesetzt als
  `"<parent_name> <sub_name> <sub_description>"`
  Beispiel: Block mit Sub-Tag `#frontend` (Beschreibung „Refactoring Login-Flow") unter Parent `#projekta` →
  `"#projekta #frontend Refactoring Login-Flow"`
  - Bei Block direkt auf Parent-Tag: nur `"<parent_name>"`.
  - Fehlt die Beschreibung: weglassen, kein Trennzeichen-Rest.
  - Bei Aggregation mehrerer Blöcke mit unterschiedlichen Sub-Tags unter gleichem Mapping: Beschreibungen mit `; ` zusammengeführt und dedupliziert.
  - Pro Block kann eine zusätzliche **Tätigkeitsbeschreibung** (`focus_blocks.description`) gesetzt sein. Existiert sie, wird sie mit `" — "` an den aus Tag/Sub-Tag generierten Kommentar angehängt; ist kein Tag-Kommentar vorhanden, ersetzt die Beschreibung diesen.
- Sync-Modi:
  - Einzelner Tag (Button im Hauptfenster)
  - Zeitraum
- Idempotenz: bereits synchronisierte Blöcke werden mit `personio_attendance_id` markiert; erneuter Sync erfordert Bestätigung (Update statt Create).
- Fehlerbehandlung: HTTP-Fehler werden geloggt und im UI angezeigt.

---

## 3. Nicht-funktionale Anforderungen

- **Performance:** CPU-Last < 1 % im Idle, RAM < 100 MB.
- **Privacy:** Keine externen Calls außer zu Personio. Alle Daten lokal.
- **Robustheit:** Crash-sicher – ungespeicherter Block wird beim nächsten Start wiederhergestellt.
- **Logging:** Rotierendes Logfile (`%LOCALAPPDATA%\TimeTracker\log\`).

---

## 4. Architektur & Design-Entscheidungen

### 4.1 Modulstruktur (Go-Packages)
```
/cmd/timetracker          Main, Tray-Setup
/internal/tracker         Fokus-Polling, Idle-Detection
/internal/winapi          Windows-API-Wrapper (CGO oder syscall)
/internal/storage         SQLite-Layer (Repository-Pattern)
/internal/ui              Hauptfenster (siehe 4.3)
/internal/tagging         Tag-Logik, Block-Zuordnung
/internal/personio        Personio-API-Client
/internal/config          Settings (TOML/JSON in %APPDATA%)
/internal/logging         strukturiertes Logging (zerolog/slog)
```

### 4.2 Bibliotheken
- **Tray:** `github.com/getlantern/systray` (etabliert, plattformübergreifend).
- **SQLite:** `modernc.org/sqlite` (pure Go, kein CGO nötig → einfacher Build) ODER `mattn/go-sqlite3` falls Performance kritisch.
- **Windows API:** `golang.org/x/sys/windows` für `GetForegroundWindow` etc.
- **UI-Fenster:** **Wails v2** (Go-Backend + Web-Frontend). Frontend-Stack: **TypeScript + React + Vite**, Styling mit **Tailwind CSS**. Timeline via `vis-timeline` oder `react-calendar-timeline`.
- **HTTP-Client:** Standard `net/http` mit Retry-Wrapper.
- **Konfiguration:** TOML mit `BurntSushi/toml`.

### 4.3 Datenmodell (SQLite)
```sql
CREATE TABLE focus_blocks (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  process_name    TEXT NOT NULL,
  process_path    TEXT,
  window_title    TEXT NOT NULL,
  start_time      DATETIME NOT NULL,
  end_time        DATETIME,
  duration_sec    INTEGER,
  is_idle         BOOLEAN DEFAULT 0,
  tag_id          INTEGER,
  auto_tagged     BOOLEAN DEFAULT 0,
  description     TEXT,                    -- freie Tätigkeitsbeschreibung pro Block
  personio_id     TEXT,
  synced_at       DATETIME,
  FOREIGN KEY (tag_id) REFERENCES tags(id)
);
CREATE INDEX idx_blocks_start ON focus_blocks(start_time);
CREATE INDEX idx_blocks_tag   ON focus_blocks(tag_id);

CREATE TABLE tags (
  id                    INTEGER PRIMARY KEY AUTOINCREMENT,
  parent_id             INTEGER,
  name                  TEXT NOT NULL,           -- Format: #alphanum
  description           TEXT,                    -- nur sinnvoll bei Sub-Tags
  color                 TEXT,
  personio_project_id   TEXT,
  personio_activity_id  TEXT,
  sync_to_personio      BOOLEAN DEFAULT 1,
  created_at            DATETIME DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (parent_id) REFERENCES tags(id) ON DELETE CASCADE,
  UNIQUE (parent_id, name),
  CHECK (name GLOB '#[A-Za-z0-9]*')
);
CREATE INDEX idx_tags_parent ON tags(parent_id);

CREATE TABLE tagging_rules (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  match_field  TEXT NOT NULL CHECK (match_field IN ('process_name','window_title','both')),
  match_type   TEXT NOT NULL CHECK (match_type IN ('contains','equals','regex')),
  pattern      TEXT NOT NULL,
  tag_id       INTEGER NOT NULL,
  priority     INTEGER NOT NULL DEFAULT 0,
  enabled      BOOLEAN DEFAULT 1,
  created_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (tag_id) REFERENCES tags(id) ON DELETE CASCADE
);
CREATE INDEX idx_rules_priority ON tagging_rules(priority DESC, enabled);

CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT
);
```
DB-Pfad: `%LOCALAPPDATA%\TimeTracker\data.db`.

### 4.4 Tracking-Loop (Pseudocode)
```
ticker := 2s
currentBlock := nil
loop:
  hwnd := GetForegroundWindow()
  pid  := GetProcessId(hwnd)
  proc := GetProcessName(pid)
  title := GetWindowText(hwnd)
  idle := IsIdle(threshold=5min)

  if idle:
    closeBlock(currentBlock)
    continue

  if currentBlock == nil OR proc != currentBlock.proc OR title != currentBlock.title:
    closeBlock(currentBlock)
    currentBlock = openBlock(proc, title, now())
```

### 4.5 Personio-Sync-Flow
1. User wählt Datum/Zeitraum → klickt „Sync".
2. Lade alle Blöcke mit zugewiesenem Tag, deren effektives Mapping (Sub-Tag oder vererbt vom Parent) `sync_to_personio = 1` hat.
3. Resolve effektives Mapping pro Block: Sub-Tag-Mapping bevorzugt, sonst Parent-Mapping.
4. Baue `comment` pro Block: `"<parent_name> <sub_name> <sub_description>"` (leere Teile auslassen).
5. Aggregiere pro `(Datum, project_id, activity_id)` → eine Attendance-Periode; Comments werden mit `; ` zusammengeführt und dedupliziert.
6. Sende Requests; bei Erfolg `personio_id` und `synced_at` pro Block speichern.
7. Zeige Erfolgs-/Fehlerübersicht.

### 4.6 Konfiguration (Beispiel `config.toml`)
```toml
[tracking]
poll_interval_sec = 2
idle_threshold_min = 5

[personio]
client_id     = ""
client_secret = ""
employee_id   = ""
base_url      = "https://api.personio.de/v1"

[ui]
autostart = true
```

---

## 5. Build & Deployment
- Single-File-Executable: `go build -ldflags="-H windowsgui"` (kein Konsolenfenster).
- Icon-Embedding via `goversioninfo`.
- Optionaler Installer mit **Inno Setup** oder **MSIX**.

---

## 6. Roadmap / Phasen für die Umsetzung
1. **Phase 1 – Tracking-Core:** Windows-API-Wrapper, Tracking-Loop, SQLite-Persistenz, Logging.
2. **Phase 2 – Tray-App:** systray-Integration, Autostart, Pause/Resume.
3. **Phase 3 – UI:** Wails-Setup, Timeline-View, Tag-Verwaltung (inkl. Hashtag-Validator und Sub-Tag-Beschreibung), Block-Editing.
4. **Phase 4 – Personio:** API-Client, OAuth, Sync-Logik, Comment-Aufbau, Idempotenz.
5. **Phase 5 – Polish:** Idle-Detection, Crash-Recovery, Installer, Icon, Tests.
6. **Phase 6 – Auto-Tagging (Ausbaustufe):** Regel-Engine, UI für Regel-Verwaltung mit Live-Test, Bulk-Apply auf historische Blöcke.

---

## 7. Offene Punkte / Annahmen
- Personio-Mapping: zweistufige Tag-Hierarchie (Parent + Sub-Tag), Sub-Tag-Mapping überschreibt Parent.
- Mehrere Monitore: `GetForegroundWindow` ist global, also unproblematisch.
- Browser-Tabs: nur via Fenstertitel erfassbar (kein DOM-Zugriff). Akzeptiert.
- Multi-User auf einem Rechner: pro Windows-User eigene DB im `LOCALAPPDATA`.

---

## 8. Akzeptanzkriterien
- Tool läuft stabil > 8 h ohne Crash.
- Fokuswechsel werden mit < 3 s Verzögerung erfasst.
- Timeline zeigt einen Tag mit > 200 Blöcken flüssig (< 500 ms Render).
- Personio-Sync eines Tages dauert < 10 s und ist idempotent.
- CPU-Last im Idle < 1 % auf Standard-Hardware.
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
- Konsekutive identische Fokussierungen (gleicher Prozess + gleicher Titel) werden zu einem Eintrag zusammengefasst.
- **Idle-Detection:** Wenn der User > 5 Min inaktiv ist (kein Input via `GetLastInputInfo`), wird der aktuelle `process_track` beendet und als „Idle" markiert.
- **Lock/Sleep:** Beim Sperren oder Ruhezustand wird der aktuelle `process_track` sauber abgeschlossen.
- **Roh-Erfassung (keine Granularität):** Der Tracker schreibt `process_tracks` mit den exakten Poll-Zeitstempeln in die DB. Es findet **keine** Granularitäts-Quantisierung auf dieser Ebene statt — Slot-Snapping ist Aufgabe der Tag-Block-Schicht (§2.4.3). Der Process-Track-Strip in der UI zeigt damit, was tatsächlich passiert ist, sekundengenau.
- **Disjoint-Eigenschaft:** Process-Tracks sind per Konstruktion disjunkt (der Tracker schließt seinen vorherigen Eintrag, bevor er einen neuen öffnet). Eine Overlap-Prüfung auf Storage-Ebene ist daher nicht nötig.
- **Crash-Recovery:** Beim Start finalisiert der Tracker **alle** Process-Tracks, die noch ohne `end_time` in der DB stehen (`ListOpen`), nicht nur den letzten. Jeder Recovery-Eintrag wird auf `min(start + idle_threshold, now, next_open.start)` geschlossen.
- **Tagging-Entkopplung:** Der Tracker entscheidet **nichts** über Tags. Bei jedem Fokuswechsel ruft er den Tagging-Orchestrator (`OnFocusChanged`) auf; der Orchestrator pflegt die `tag_blocks` in einer separaten Tabelle (siehe §2.4.3).

### 2.2 Tray-Icon
- Tool startet minimiert in der Windows-Taskleiste (System Tray).
- **Linksklick** auf Icon → öffnet Hauptfenster mit Zeitachse.
- **Rechtsklick** → Kontextmenü mit:
  - Öffnen
  - Pause Tracking (Checkbox; spiegelt `tracking.enabled` wider)
  - Sync zu Personio (heute)
  - **Manueller Tag** (Submenü mit „Kein Tag (Stop)" + einem Eintrag pro
    konfiguriertem Tag, siehe 2.4.2)
  - Autostart (Checkbox)
  - Über `<version>`
  - Beenden
- **Autostart**-Option (Registry-Eintrag unter `HKCU\...\Run`).
- Die Tag-Liste im Submenü wird beim Tray-Start erfasst — neu angelegte
  Tags erscheinen erst nach Neustart der Anwendung.

### 2.3 Hauptfenster (Zeitachse)
- Tagesansicht: **zwei übereinander liegende Strips** mit gemeinsamer
  Zoom/Scroll-Achse, darunter zwei Tabellen.
- Datums-Navigation (Vor/Zurück, Datepicker).
- **Top-Strip — Tag-Blöcke:** Visualisiert die manuellen und automatischen
  `tag_blocks`. Auto-Blöcke werden mit gestrichelter Umrandung dargestellt,
  manuelle Blöcke ohne Rand. Das Strip ist die einzige Stelle, an der
  manuelles Tagging gestartet wird (Drag-to-tag).
- **Bottom-Strip — Process-Tracks:** Read-only-Visualisierung der rohen
  Fenster-Fokus-Events. Idle-Bereiche werden abgeblendet; jeder Prozess
  bekommt eine deterministische Hue, damit aufeinanderfolgende Programme
  visuell trennbar sind. Granularität greift hier nicht — die Zeiten sind
  sekundengenau.
- **Drag auf dem Top-Strip** zieht eine Zeitspanne auf (snappt live auf
  das Granularitätsraster). Beim Loslassen entsteht ein „committed range".
  Ein Klick auf einen Tag im Auswahl-Panel ruft `CreateManualRange` auf:
  überlappende Auto-Tag-Blöcke werden getrimmt/gesplittet/gelöscht (Manual
  schlägt Auto), Überschneidung mit einem bestehenden manuellen Tag-Block
  wird mit `ErrOverlap` abgelehnt.
- **Drag-Range-Edit:** Die Kanten der noch ungetaggten Range lassen sich
  greifen und neu positionieren (Snap auf das Granularitätsraster).
- **Klick auf einen Tag-Block** im Top-Strip selektiert ihn (Shift = additiv).
  Ausgewählte Blöcke können neu getagt werden (`SetTagBlockTag`),
  beschriftet (`SetTagBlockDescription`) oder gelöscht (`DeleteTagBlock`).
- **Tag-Picker im Auswahl-Panel:** Die Tag-Buttons werden **nach Eltern-Tag
  gruppiert** gerendert (Eltern zuerst, danach unmittelbar dessen Sub-Tags).
  Sub-Tag-Buttons stellen den Eltern-Namen als gedimmtes Präfix
  („`<Parent> › <Sub>`") voran, damit gleichnamige Sub-Tags unterschiedlicher
  Eltern (z. B. `#projekta › #meeting` vs. `#projektb › #meeting`)
  unterscheidbar bleiben. Der vollständige Pfad steht zusätzlich im
  Tooltip (`title`).
- **Hover** auf einem Tag-Block oder Process-Track filtert die untere
  Process-Tabelle auf den Zeitraum.
- **Mausrad zoomt** (cursor-anchored), **Shift+Mausrad schwenkt**,
  **Doppelklick** setzt den Zoom zurück. Beide Strips reagieren synchron
  auf dieselbe View-Window-State.
- **Tabellen** unterhalb der Strips:
  - Tag-Block-Tabelle: pro `tag_block` eine Zeile mit Zeitraum, Dauer,
    `manuell|auto`, Beschreibung, Tag-Chips (Parent + Sub).
  - Process-Track-Tabelle: gruppiert aufeinanderfolgende Tracks mit
    gleichem Prozess. Der Pfeil expandiert die Gruppe und zeigt die
    einzelnen Fenstertitel.

### 2.3.1 Tätigkeitsbeschreibung pro Tag-Block
- Jeder `tag_block` hat ein optionales freies Textfeld `description`.
- Beim Anlegen einer manuellen Range („Tag-Button" im Auswahl-Panel) wird
  der Beschreibungstext mit übernommen.
- Bei einem **offenen** manuellen Tag (siehe §2.4.2) wird die beim Start
  hinterlegte Beschreibung auch nach einer Auto-Tag-Unterbrechung erneut
  in den fortgesetzten manuellen Block geschrieben.
- Beim Personio-Sync wird die Beschreibung an den aus Tag/Sub-Tag
  generierten Kommentar angehängt (Format: `"<tag-comment> — <description>"`).

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

### 2.4.1 Auto-Tagging (Tag-Block-Lebenszyklus)
- Regelbasierte automatische Tag-Zuweisung als **eigener Tag-Block** im
  `tag_blocks`-Schema, gepflegt vom Tagging-Orchestrator
  (`internal/tagging/orchestrator.go`).
- Eine Regel besteht aus:
  - `match_field`: `process_name` | `window_title` | `both`
  - `match_type`: `contains` | `equals` | `regex`
  - `pattern`: Suchstring/Regex
  - `tag_id`: Ziel-Tag (Parent oder Sub)
  - `priority`: Integer; höhere Priorität gewinnt bei Mehrfach-Match
  - `enabled`: bool
- **Lebenszyklus:**
  1. Tracker meldet `OnFocusChanged(name, title, at)`.
  2. Orchestrator prüft Regeln in Priorität-DESC-Reihenfolge.
  3. Trifft eine Regel: ist bereits ein Auto-Block der **gleichen Regel**
     offen, wird er weitergeführt. Sonst wird ein offener Auto-Block der
     anderen Regel geschlossen (Floor-Snap auf Granularität, siehe §2.4.3)
     und ein neuer Auto-Block für die treffende Regel geöffnet (Floor-Snap
     auf Granularität).
  4. Trifft keine Regel: ein offener Auto-Block wird geschlossen.
- **Regex-Engine:** Go-Standardbibliothek `regexp` (RE2-Syntax) — kein
  Backtracking, lineare Laufzeit. Ungültige Patterns werden beim Speichern
  via `regexp.Compile` validiert und abgelehnt.
- **Zero-Length-Suppression:** Floor-Snap am Anfang **und** am Ende kann
  einen 0-Sekunden-Auto-Block produzieren (wenn ein Match die Granularität
  unterschreitet). Solche Blöcke werden nicht persistiert / vor dem Close
  wieder gelöscht.
- **Vorrang manueller Range-Tags:** §2.4.3 beschreibt das Verhalten bei
  Überlappung — eine manuelle Range schneidet Auto-Blöcke aus.

### 2.4.2 Manuelles Tagging (offene-Ende-Sitzung)
- Über das Tray-Submenü „Manueller Tag" startet der User eine **offene
  manuelle Tag-Sitzung**. Anders als ein Range-Tag hat sie kein Ende-Datum
  und gilt als Default-Kontext, in den die laufende Aktivität automatisch
  einsortiert wird.
- **Genau einer** offener manueller Tag-Block existiert zu jeder Zeit.
  Beim Anwendungsstart schließt der Orchestrator alle dangling offenen
  manuellen Blöcke (`CloseDanglingManualAtStartup`) auf das Ende des
  letzten Process-Tracks (oder `now`, wenn kein Tracking lief).
- **Auto-Tag-Unterbrechung:**
  1. User startet manuellen Tag M zur Zeit T1.
  2. Aktivität läuft, M ist offen und „aktiv".
  3. Zur Zeit T2 fokussiert ein Prozess, der eine Auto-Tag-Regel erfüllt:
     M wird auf `floor(T2)` geschlossen, ein Auto-Block A öffnet auf
     `floor(T2)`, und der Orchestrator merkt sich M's Tag + Beschreibung
     als „pausiert".
  4. Auto-Block A bleibt offen, solange ein matching-Prozess läuft.
  5. Endet das Matching zu T3 (anderer Prozess oder Idle), schließt A auf
     `floor(T3)`. Ist die Granularität so groß, dass `floor(T3) ==
     floor(T2)`, wird A gelöscht statt geschlossen (zero-length).
  6. Sofort danach wird ein **neuer** manueller Tag-Block mit dem gleichen
     Tag und derselben Beschreibung wie M auf `floor(T3)` geöffnet —
     vorausgesetzt, der Fokus zeigt auf einen aktiven Prozess. Bei Idle
     bleibt M „pausiert", bis der nächste Fokus-Event kommt.
- **Start während aktiver Auto-Sitzung:** Klickt der User „Manueller Tag",
  während ein Auto-Block läuft, wird kein manueller Block erzeugt — der
  gewünschte Tag/Beschreibung werden als „pausiert" gespeichert und der
  manuelle Block startet erst, wenn der Auto-Block endet.
- **Stop:** „Kein Tag (Stop)" schließt den offenen manuellen Block (oder
  verwirft den pausierten Status, wenn aktuell ein Auto-Block läuft).

### 2.4.3 Granularität & manuelle Range-Tags
- **Granularität wirkt nur auf `tag_blocks`.** Process-Tracks bleiben
  immer roh.
- Granularitätsraster: lokal-zeitausgerichtet (`:00/:15/:30/:45` bei `15`),
  Anker an lokaler Mitternacht.
- **Auto-Blöcke:** Start auf Floor, End auf Floor. Zero-Length wird unter-
  drückt (siehe §2.4.1).
- **Manuelle Range-Tags:** Drag-to-tag im Top-Strip. Start floors, End
  ceils. Beim Persistieren werden überlappende Blöcke wie folgt behandelt:
  - Manueller Block in der Range: **Ablehnen** (`ErrOverlap`).
  - Auto-Block vollständig in der Range: **Löschen**.
  - Auto-Block startet vor der Range, endet in der Range: **End auf
    Range-Start trimmen** (`SetEnd`).
  - Auto-Block startet in der Range, endet nach der Range: **Start auf
    Range-Ende trimmen** (`SetStart`).
  - Auto-Block umschließt die Range vollständig: **Splitten** — Original
    wird auf Range-Start verkürzt, ein neuer Block deckt Range-Ende bis
    Original-Ende ab.
- **Manuelle Range bei aktiver offener manueller Sitzung:** kollidiert
  per Definition (offener Manual-Block ist „is_manual = 1"); die Range
  wird abgelehnt. User muss den offenen Manual zuerst stoppen.

### 2.5 Personio-Synchronisation

**Hintergrund:** Personios *öffentliche* Attendance-API (`POST /v1/company/attendances`) erlaubt nur **firmenweite** OAuth-Credentials, keine pro-Mitarbeiter-Auth. Da der TimeTracker für die Endanwender-Hand gedacht ist, wird stattdessen die **interne UI-API** angesprochen, die auch die Personio-Web-Oberfläche selbst verwendet. Der Auth-Flow ahmt einen normalen Browser-Login nach.

#### 2.5.1 Auth-Flow (CDP-getriebenes Login)
1. User trägt im **Einstellungen-Tab** seine **Tenant-Subdomain** ein (z. B. `onesi`).
2. Klick auf „Bei Personio anmelden" startet eine **eigene Chrome-Instanz** (über `chromedp`/Chrome DevTools Protocol) auf `https://<tenant>.personio.de/login/index`.
3. Der User loggt sich interaktiv ein (E-Mail, Passwort, ggf. MFA, ggf. SSO-Redirect).
4. Der TimeTracker pollt die `Page.frameNavigated`-Events: sobald die URL nicht mehr unter `/login` liegt und auf `<tenant>.personio.de` verbleibt, gilt der Login als erfolgreich.
5. Mittels `Network.GetCookies` werden alle Cookies der Domain ausgelesen, das Chrome-Fenster wird geschlossen.
6. **Validierung:** anonymer GET-Request gegen `https://<tenant>.personio.de/` mit den erfassten Cookies. Folgt Personio mit `30x → /login`, ist die Session ungültig; sonst gültig.
7. Persistenz: das Session-Blob (`tenant`, `employee_id`, `cookies[]`, `captured_at`) wird verschlüsselt im **Windows Credential Manager** unter `TimeTracker.PersonioSession` abgelegt. Die `config.toml` enthält **keine** Auth-Daten.
8. Einmalig wird `GET /api/v1/navigation/context` aufgerufen, um die Mitarbeiter-ID des Users in die Session zu schreiben.

#### 2.5.2 UI-API-Endpunkte (verifiziert per HAR-Capture)

Personio betreibt zwei verschiedene Domains: `<tenant>.personio.de` ist die Login-/Marketing-Subdomain, `<tenant>.app.personio.com` ist die eigentliche App-Shell, gegen die alle UI-API-Calls laufen. Der TimeTracker erfasst beim Login den **AppHost** (z. B. `example.app.personio.com`) aus dem post-Login-`window.location.host` und persistiert ihn in der Session.

Pro Request:
- Cookies via `cookiejar` automatisch angehängt.
- Header **`x-athena-xsrf-token`** wird aus dem XSRF-Cookie (URL-dekodiert) gespiegelt. Fallback: jedes Cookie, dessen Name `xsrf` oder `csrf` enthält.
- `Origin` und `Referer` zeigen auf `https://<app_host>`.

| Methode + Pfad | Zweck |
| --- | --- |
| `GET /api/v1/navigation/context` | Eigene Mitarbeiter-ID auflösen (`data.user.id`). |
| `GET /svc/attendance-bff/v1/timesheet/{employee_id}?start_date=YYYY-MM-DD&end_date=YYYY-MM-DD&timezone=Europe%2FBerlin&source=OVERTIME_SERVICE` | Pro Tag im Zeitraum: `day_id`, `state` (`trackable` / `locked` / `non_trackable` / …), bestehende `periods`. |
| `PUT /svc/attendance-api/v1/days/{day_id}?autoFix=true&usedInTimesheet=true` | Perioden eines Tages setzen (Upsert über UUID). |
| `DELETE /svc/attendance-api/v1/days/{day_id}` | Tag löschen (aktuell nicht vom Sync verwendet). |

**day_id-Auflösung:** Das Timesheet liefert pro Tag entweder eine bestehende `day_id` oder einen leeren Wert. Hat der Tag noch keinen Personio-Datensatz und ist `trackable`, **generiert der Client clientseitig eine UUID v4** und sendet damit den PUT — der Endpunkt erzeugt den Datensatz dann. Tage mit `state=non_trackable` (Wochenenden, Sperren) werden als „nicht buchbar" mit Fehler übersprungen.

**Body von `PUT /svc/attendance-api/v1/days/{day_id}`:**
```json
{
  "employee_id": 10076878,
  "periods": [
    {
      "id":             "<uuidv4>",
      "comment":        "#projekta #frontend — Refactoring Login-Flow",
      "period_type":    "work",
      "project_id":     4711,
      "start":          "2026-04-25T08:00:00",
      "end":            "2026-04-25T09:00:00",
      "auto_generated": false
    }
  ],
  "original_periods": [...same...],
  "geolocation":      null,
  "is_from_clock_out": false
}
```

**Zeitformat:** lokal-naiv (`YYYY-MM-DDTHH:MM:SS`, kein `Z`/Offset). Timezone wird über den `timezone`-Parameter im Timesheet-Request gesetzt; Default `Europe/Berlin`.

> Die UI-API kennt im Period-Modell **keine Activity-ID**. `tags.personio_activity_id` bleibt deshalb als **Legacy-Feld** im Schema/UI bestehen, wird aber beim Sync nicht verwendet. Sollte Personio das Feld in einer späteren UI-Version wieder einführen, kann der Syncer es ohne Schemaänderung berücksichtigen.

#### 2.5.3 Aggregation
- Quelle ist die `tag_blocks`-Tabelle (nicht mehr `focus_blocks`). Die
  Granularität ist auf dieser Ebene bereits angewandt — die Aggregation
  rundet **nicht** zusätzlich.
- Blöcke werden chronologisch durchlaufen; **konsekutive** Tag-Blöcke
  mit identischem `(lokalem Datum, project_id, comment)` und einem
  Zeitabstand ≤ 5 s werden zu **einer** Period zusammengefasst. Eine
  Lücke darüber oder ein nicht-syncbarer Block beendet den Lauf.
- Pro Period: `start` = früheste Tag-Block-Startzeit des Laufs, `end` =
  späteste Tag-Block-Endzeit (UTC → lokal-naive `YYYY-MM-DDTHH:MM:SS`).
- `period_type` ist immer `"work"`. Pausen werden aktuell nicht synchronisiert.
- Kommentar-Format: `"<parent_name> <sub_name> <sub_description>"` aus dem
  Tag-Mapping, plus optional ` — <block_description>` aus
  `tag_blocks.description`. Innerhalb eines Laufs gehören alle Blöcke per
  Konstruktion zum selben Kommentar.

#### 2.5.4 Fehlerbehandlung
- Antwort `401`/`403` oder `30x → /login` ⇒ `ErrSessionExpired`. Im UI: rotes Banner mit Hinweis auf erneute Anmeldung; Tray-Sync schreibt nur ins Log.
- Antwort `4xx` (z. B. Tag nicht buchbar) ⇒ Eintrag im `Result.Errors` mit Datum + Statusmeldung.
- Auth-Header (`X-CSRF-Token`, Cookies) werden **nie** geloggt.

#### 2.5.5 Sync-Modi & Idempotenz
- Einzelner Tag (Timeline-Button + Tray-„Sync zu Personio (heute)").
- Zeitraum (`SyncRange`) — aktuell intern, im UI nicht exponiert.
- Personio's `PUT day` ist idempotent (ersetzt den Tag). Bereits
  synchronisierte Tag-Blöcke werden lokal mit `synced_at` und der `day_id`
  als `personio_id` markiert (`tag_blocks` trägt diese Felder). Erneuter
  Sync überschreibt den Personio-Tag mit dem aktuellen Stand; manuelle
  Änderungen in Personio gehen dabei verloren.

---

## 3. Nicht-funktionale Anforderungen

- **Performance:** CPU-Last < 1 % im Leerlauf, RAM < 100 MB.
- **Datenschutz:** Keine externen Aufrufe außer zu Personio. Alle Daten verbleiben lokal.
- **Robustheit:** Absturzsicher – ein offen gebliebener Block wird beim nächsten Start automatisch finalisiert.
- **Protokollierung:** Rotierende Logdatei unter `%LOCALAPPDATA%\TimeTracker\log\`.

---

## 4. Architektur & Design-Entscheidungen

### 4.1 Modulstruktur (Go-Packages)
```
/cmd/timetracker          Main, Tray-Setup
/internal/tracker         Fokus-Polling, Idle-Detection, schreibt process_tracks
/internal/winapi          Windows-API-Wrapper
/internal/storage         SQLite-Layer (Repository-Pattern, getrennte
                          Repos für process_tracks und tag_blocks)
/internal/tagging         Tagging-Engine (Regel-Match) + Orchestrator
                          (tag_block-Lebenszyklus, manuelle Sitzungen)
/internal/app             Wails-Bindings (App-Struct mit JS-exponierten
                          Methoden für beide Strips + manuelles Tagging)
/internal/personio        Personio-API-Client (liest tag_blocks)
/internal/config          Settings (TOML in %APPDATA%)
/internal/logging         slog-Setup
```

### 4.2 Bibliotheken
- **Tray:** `github.com/getlantern/systray` (etabliert, plattformübergreifend).
- **SQLite:** `modernc.org/sqlite` (pure Go, kein CGO nötig → einfacher Build) ODER `mattn/go-sqlite3` falls Performance kritisch.
- **Windows API:** `golang.org/x/sys/windows` für `GetForegroundWindow` etc.
- **UI-Fenster:** **Wails v2** (Go-Backend + Web-Frontend). Frontend-Stack: **TypeScript + React + Vite**, Styling mit **Tailwind CSS**. Timeline via `vis-timeline` oder `react-calendar-timeline`.
- **HTTP-Client:** Standard `net/http` (Cookie-Jar über `net/http/cookiejar`).
- **Personio-Login (CDP):** `github.com/chromedp/chromedp` steuert eine reale Chrome-Instanz für den interaktiven Login. Das System-Chrome auf dem Host muss installiert sein.
- **Konfiguration:** TOML mit `BurntSushi/toml`.

### 4.3 Datenmodell (SQLite)

> **Migration 0004** hat das frühere `focus_blocks` (Mischung aus Prozess-
> Aktivität und Tagging-Status) in zwei unabhängige Tabellen gespalten:
> `process_tracks` (rohe Fokus-Events) und `tag_blocks` (Tagging-Spannen).
> Bestehende Daten werden in beide Tabellen migriert; die `down`-Migration
> fügt sie wieder zusammen.

```sql
-- Rohe Fokus-Events. Disjunkt per Konstruktion, keine Granularität,
-- keine Tagging-Felder.
CREATE TABLE process_tracks (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  process_name  TEXT NOT NULL,
  process_path  TEXT,
  window_title  TEXT NOT NULL,
  start_time    DATETIME NOT NULL,
  end_time      DATETIME,
  duration_sec  INTEGER,
  is_idle       INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_process_tracks_start ON process_tracks(start_time);
CREATE INDEX idx_process_tracks_open  ON process_tracks(end_time)
  WHERE end_time IS NULL;

-- Tagging-Spannen. Granularität ist auf dieser Ebene bereits angewandt.
-- Non-Overlap-Invariante: jeder Open/SetEnd/SetStart prüft per
-- `selectTagBlockOverlap`, ob das neue Intervall einen anderen Tag-Block
-- schneidet (offene Blöcke gelten als bis ∞ laufend). Bei Konflikt wird
-- `ErrOverlap` zurückgegeben.
CREATE TABLE tag_blocks (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  tag_id        INTEGER NOT NULL,
  description   TEXT,
  start_time    DATETIME NOT NULL,
  end_time      DATETIME,
  duration_sec  INTEGER,
  is_manual     INTEGER NOT NULL DEFAULT 0,
  personio_id   TEXT,
  synced_at     DATETIME,
  FOREIGN KEY (tag_id) REFERENCES tags(id) ON DELETE CASCADE
);
CREATE INDEX idx_tag_blocks_start ON tag_blocks(start_time);
CREATE INDEX idx_tag_blocks_tag   ON tag_blocks(tag_id);
CREATE INDEX idx_tag_blocks_open  ON tag_blocks(end_time)
  WHERE end_time IS NULL;

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
// Beim Start: alle Process-Tracks aus vorherigen Runs finalisieren.
for open in tracks.ListOpen():
  end = min(open.start + idle_threshold, now, next_open.start)
  tracks.Close(open.id, end)        // rohe Zeit, kein Snap
// Orchestrator finalisiert dangling tag_blocks separat.
orchestrator.Recover()
orchestrator.CloseDanglingManualAtStartup(fallback = now)

ticker := 2s   // konfigurierbar via tracking.poll_interval_sec
current := nil
loop:
  hwnd := GetForegroundWindow()
  proc := GetProcessName(GetProcessId(hwnd))
  title := GetWindowText(hwnd)
  idle := IsIdle(threshold=5min)

  if idle:
    tracks.MarkIdle(current.id, now())
    orchestrator.OnFocusCleared(now())
    continue

  if current == nil OR proc != current.proc OR title != current.title:
    tracks.Close(current.id, now())
    current = tracks.Open(proc, title, now())
    orchestrator.OnFocusChanged(proc, title, now())
```

> Der Tracker selbst kennt keine Granularität und keine Tags — er meldet
> Fokus-Änderungen an den Orchestrator, der `tag_blocks` mit
> Granularitäts-Snap und Lifecycle-Logik pflegt.

### 4.4.1 Orchestrator-State-Machine (Pseudocode)
```
// Internal state, guarded by mu:
//   focusActive:      bool
//   openAuto:         { blockID, ruleID, tagID } | nil
//   openManual:       { blockID, tagID, description } | nil
//   pausedManual:     { tagID, description } | nil

OnFocusChanged(name, title, at):
  focusActive = true
  rule = matchRule(name, title)        // first-by-priority enabled rule
  advance(at, rule)

OnFocusCleared(at):
  focusActive = false
  advance(at, nil)

advance(at, rule):
  snap = floor(at, granularity)

  if openAuto != nil:
    if rule != nil and rule.id == openAuto.ruleID:
      return                            // same rule, keep open
    closeAuto(snap)                     // may resume pausedManual

  if rule != nil:
    if openManual != nil:
      pauseManual(snap)                 // remembers tag+desc, closes block
    startAuto(rule, snap)
    return

  if !focusActive and openManual != nil:
    pauseManual(snap)
    return

  if pausedManual != nil and openManual == nil and focusActive:
    resumeManual(snap)                  // opens fresh manual at `snap`

closeAuto(snap):
  // delete if zero-length, otherwise SetEnd(snap)
  // resume pausedManual if focusActive
```

### 4.5 Personio-Sync-Flow
1. User wählt Datum/Zeitraum → klickt „Sync".
2. Lade alle `tag_blocks` im Zeitraum, deren effektives Mapping (Sub-Tag
   oder vererbt vom Parent) `sync_to_personio = 1` hat. Offene Tag-Blöcke
   werden übersprungen (kein `end_time`).
3. Resolve effektives Mapping pro Tag-Block: Sub-Tag-Mapping bevorzugt,
   sonst Parent-Mapping.
4. Baue `comment` pro Tag-Block: `"<parent_name> <sub_name> <sub_description>"`
   plus optional ` — <description>` (leere Teile auslassen).
5. Aggregiere konsekutive Tag-Blöcke mit identischem `(lokalem Datum,
   project_id, comment)` zu **einer** Period (siehe §2.5.3).
6. Hole pro Tag das Timesheet (`day_id`, `state`); bei `state=trackable`
   PUT mit den aggregierten Perioden, sonst Fehler ins Result.
7. Sende Requests; bei Erfolg `personio_id` (= `day_id`) und `synced_at`
   auf jedem beteiligten `tag_block` speichern.
8. Zeige Erfolgs-/Fehlerübersicht.

> Die Non-Overlap-Invariante (§4.3) auf `tag_blocks` ist Voraussetzung für
> diesen Ablauf: Personio antwortet auf überlappende Perioden mit
> `400 — There are overlapping WORK periods`, und der gesamte Tag bleibt
> dann unverändert.

### 4.6 Konfiguration (Beispiel `config.toml`)
```toml
[tracking]
enabled                    = true   # globaler Schalter: false pausiert Polling + Auto-Tagging
poll_interval_sec          = 2
idle_threshold_min         = 5
tag_block_granularity_min  = 0      # 0 = aus; 15 = Tag-Blöcke (manuell + auto)
                                    # auf 15-min-Slots snappen (§2.4.3).
                                    # Process-Tracks bleiben immer roh.

[personio]
tenant = "onesi"            # Subdomain, der Login läuft via CDP — keine API-Tokens

[ui]
autostart = true
```

> Auth-Daten (Personio-Cookies, XSRF-Token) liegen **nicht** in `config.toml`,
> sondern verschlüsselt im Windows Credential Manager unter
> `TimeTracker.PersonioSession`.

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
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
- **Disjoint-Eigenschaft:** Fokus-Process-Tracks (`is_communication = 0`) sind per Konstruktion disjunkt (der Tracker schließt seinen vorherigen Eintrag, bevor er einen neuen öffnet). Eine Overlap-Prüfung auf Storage-Ebene ist daher nicht nötig. Kommunikations-Tracks (§2.1a) überlappen sich bewusst — sowohl mit Fokus-Tracks als auch untereinander.
- **Crash-Recovery:** Beim Start finalisiert der Tracker **alle** Process-Tracks, die noch ohne `end_time` in der DB stehen (`ListOpen` für Fokus-Tracks, `ListOpenCommunication` für Kommunikations-Tracks), nicht nur den letzten. Fokus-Recovery-Einträge werden auf `min(start + idle_threshold, now, next_open.start)` geschlossen, Kommunikations-Recovery-Einträge auf `min(start + idle_threshold, now)` (untereinander gibt es keine Reihenfolge).
- **Tagging-Entkopplung:** Der Tracker entscheidet **nichts** über Tags. Bei jedem Fokuswechsel ruft er den Tagging-Orchestrator (`OnFocusChanged`) auf, bei jeder Änderung an der Menge aktiver Kommunikations-Fenster `OnCommunicationChanged`; der Orchestrator pflegt die `tag_blocks` in einer separaten Tabelle (siehe §2.4.3).

### 2.1a Kommunikations-Tracking (parallel zum Fokus)
- **Zweck:** Hybrid-Worker, die in Teams/Zoom/… an Meetings teilnehmen oder
  ihren Bildschirm teilen, sollen diese Zeit auch dann zugeordnet bekommen,
  wenn sie nebenbei in einem anderen Fenster arbeiten (Recherche im Browser,
  Notizen im Editor). Klassisches Fokus-Tracking würde dabei die Auto-Tag-Regel
  des Fokus-Fensters greifen lassen — fachlich falsch.
- **Konfiguration:**
  - `[communication] process_names = ["teams.exe"]` (default). Liste
    case-insensitive-verglichener `process_name`-Basenamen.
  - `[communication] title_exclude_phrases = []` (default). Globale
    Ausschluss-Liste: enthält der Fenstertitel eines Comm-Prozess-Fensters
    einen dieser Strings (case-insensitive Substring-Match; leere/whitespace-
    only-Einträge werden beim Speichern verworfen, Reihenfolge bleibt erhalten),
    wird das Fenster **nicht** als Kommunikations-Fenster behandelt — es
    taucht weder auf der Comm-Schiene auf, noch löst es Comm-getriebene
    Auto-Tags (§2.4.1a) aus. Das Fenster wird wie jeder andere Prozess vom
    Fokus-Tracker erfasst, sobald es im Fokus steht.
  - Beide Listen sind hot-reloadable über die Settings-UI; SaveConfig ruft
    `tracker.SetCommunicationNames` und `tracker.SetTitleExcludePhrases` auf.
- **Erkennungssignal:** Sichtbares Top-Level-Fenster, das einem der
  konfigurierten Prozesse gehört (`EnumWindows` + `IsWindowVisible` +
  `GetWindowThreadProcessId` + Basename-Match) **und dessen Fenstertitel
  keine der in `title_exclude_phrases` konfigurierten Phrasen enthält**.
  Versteckte Background-Fenster (Teams läuft auch ohne Meeting im Tray)
  erzeugen **keinen** Track — ausschlaggebend ist die Sichtbarkeit gegenüber
  dem User. Die Exclude-Prüfung wird in jedem Tick neu evaluiert: wechselt
  der Titel zur Laufzeit auf einen ausgeschlossenen Wert, schließt der
  Tracker den offenen Comm-Track auf `now`; wechselt er später wieder
  zurück auf einen zulässigen Titel, öffnet ein **neuer** Comm-Track.
  Ausgeschlossene Fenster verhalten sich exakt wie reguläre Programme —
  Fokus-Tracking und fokus-getriebene Auto-Tag-Regeln greifen normal.
- **Datenmodell:** Kommunikations-Tracks landen in derselben
  `process_tracks`-Tabelle, markiert mit `is_communication = 1`. Sie
  überlappen Fokus-Tracks und sich untereinander. Die UI rendert sie auf
  einer dedizierten Timeline-Schiene mit Telefon-Symbol.
- **Lifecycle:** Pro sichtbarem, nicht-ausgeschlossenem Fenster
  (PID + HWND) ein offener Track. Titel-Änderung schließt den alten
  Track und öffnet einen neuen (gleiche Semantik wie bei Fokus-Tracks).
  Wechselt der neue Titel in den Exclude-Bereich, wird der Track
  geschlossen und **kein** neuer eröffnet, bis der Titel wieder zulässig
  ist. Verschwindet das Fenster (X, in den Tray minimiert, Anruf beendet,
  Prozess gestorben), wird der Track sofort geschlossen. Idle-Threshold
  und Lock-Screen wirken **nicht** auf Kommunikations-Tracks — der User,
  der nur einem Meeting zuhört, soll trotz fehlender Tastatureingaben
  weiter erfasst werden.
- **Pause-Toggle:** Der Tracker schließt mit `Pause` ebenfalls alle
  offenen Kommunikations-Tracks; "Pause Tracking" bedeutet
  konsequent „keine Erfassung mehr".

### 2.2 Tray-Icon
- Tool startet minimiert in der Windows-Taskleiste (System Tray).
- **Linksklick** auf Icon → öffnet Hauptfenster mit Zeitachse.
- **Rechtsklick** → Kontextmenü mit:
  - Öffnen
  - Pause Tracking (Checkbox; spiegelt `tracking.enabled` wider)
  - Sync zu Personio (heute)
  - **Manueller Tag** (Submenü mit „Kein Tag (Stop)" + einem Eintrag pro
    konfiguriertem Tag, siehe 2.4.2)
  - Über `<version>`
  - **Hilfe** — öffnet das Hauptfenster und wechselt in den Tab
    *Hilfe* (siehe §2.3a). Backend-Methode `App.OpenHelpTab` ruft
    `ShowWindow` und feuert das Wails-Event `help:open` an das
    Frontend.
  - Beenden (echter Exit; offene Process-Tracks und Tag-Blöcke werden vorher
    geschlossen, ein Sync findet **nicht** statt — der Auto-Sync läuft beim
    nächsten Start, siehe §2.5.6)
- **Autostart** wird ausschließlich vom MSI-Installer gesetzt
  (`HKCU\...\Run`-Eintrag, siehe §5.2). Die Anwendung selbst bietet
  keinen Toggle mehr; Anwender, die den Autostart unterdrücken wollen,
  entfernen den Run-Eintrag manuell.
- Die Tag-Liste im Submenü wird beim Tray-Start erfasst — neu angelegte
  Tags erscheinen erst nach Neustart der Anwendung.
- **Submenü-Reihenfolge:** Tags werden **nach Eltern-Tag gruppiert**
  (Eltern-Tag zuerst, danach unmittelbar dessen Sub-Tags). Sub-Tag-Einträge
  tragen den Eltern-Namen als Präfix („`<Parent> › <Sub>`"), damit
  gleichnamige Sub-Tags unterschiedlicher Eltern eindeutig wählbar bleiben.

### 2.2a Quick-Tag-Picker (globaler Hotkey)

- Hashpoint registriert auf Wunsch einen **globalen Win32-Hotkey**
  (`RegisterHotKey` auf einem dedizierten OS-gelockten Message-Loop-Thread
  in `internal/winapi/hotkey_windows.go`). Drücken des Hotkeys von einer
  beliebigen Anwendung aus öffnet einen kleinen, kompakten Tag-Picker.
- **Default-Hotkey:** `Ctrl+Alt+T`. Konfigurierbar in den Einstellungen
  (`config.QuickTag.Hotkey`). Format: `<Mod>+<Mod>+<Taste>` mit
  Modifiern `Ctrl`, `Alt`, `Shift`, `Win` und Tasten `A–Z`, `0–9`,
  `F1–F24`. Mindestens ein Modifier ist Pflicht. `Win+T` ist eine
  Windows-Shell-Tastenkombination (Taskleisten-Fokus) und wird in
  Doku/UI explizit als „nicht empfohlen" markiert.
- **Aktivierung:** Über `config.QuickTag.Enabled` (Default `true`).
  Validierung lehnt ungültige Hotkey-Strings beim Speichern ab; ist die
  Registrierung zur Laufzeit fehlgeschlagen (z. B. weil eine andere
  App denselben Hotkey beansprucht), wird das auf `Warn`-Level geloggt
  und der Picker ist stumm.
- **Picker-Fenster:** Wails v2 unterstützt nur ein Fenster — der Picker
  übernimmt deshalb das Hauptfenster für die Dauer der Auswahl. Beim
  Hotkey-Druck speichert das Backend Größe und Position des Hauptfensters,
  setzt es auf `340×420` Pixel an die untere rechte Ecke des Monitors,
  auf dem sich der Cursor befindet (`MonitorFromPoint(GetCursorPos())`,
  Work-Area), schaltet `AlwaysOnTop` ein und feuert das Wails-Event
  `quick-tag-picker:open` an das Frontend. Beim Schließen wird Größe,
  Position und `AlwaysOnTop` restauriert; war das Hauptfenster vorher
  versteckt (Tray-Modus, geprüft via `OnBeforeClose`-Hook), wird es
  wieder versteckt.
- **Inhalt:** Bis zu 10 Einträge, nummeriert `0`–`9`. Reihenfolge:
  zuerst die zuletzt verwendeten Tags der **letzten 30 Tage** (sortiert
  absteigend nach `MAX(start_time)` in `tag_blocks`), dann mit unbenutzten
  Tags in derselben Eltern-zuerst-Reihenfolge wie das Tray-Submenü und
  der Timeline-Picker aufgefüllt. Sub-Tags tragen den Eltern-Namen als
  Präfix (`<Parent> › <Sub>`). Der aktuell offene oder pausierte
  manuelle Tag wird hervorgehoben (`is_active` im DTO, „aktiv"-Badge im UI).
- **Auswahl:** Tasten `0`–`9` oder Mausklick. `Esc` schließt ohne
  Wechsel. Eine erneute Hotkey-Betätigung bei offenem Picker wirkt wie
  `Esc` (Toggle).
- **Wirkung:** Der gewählte Tag wird per `App.QuickTagSelect(tag_id)`
  aktiviert. Wählt der User den bereits aktiven Tag, ist die Aktion
  ein No-op (der laufende Block wird nicht neu gestartet). Bei einem
  neuen Tag ruft das Backend `Orchestrator.StartManualOpenEnded` auf
  — der bestehende manuelle Block wird also auf das Granularitätsraster
  geschlossen, bevor der neue startet (siehe §2.4.2).
- **Plattform:** Nur Windows. Der `winapi`-Stub auf anderen Plattformen
  liefert `ErrUnsupported`; das Feature ist dort wirkungslos.

### 2.3 Hauptfenster (Zeitachse)
- Hauptfenster startet **maximiert** (`WindowStartState: Maximised`),
  damit beide Listen direkt nebeneinander Platz haben. Das Fenster bleibt
  ein normales Fenster (Titelleiste, verschiebbar, verkleinerbar) — kein
  echter Fullscreen.
- Tagesansicht: **zwei übereinander liegende Strips** mit gemeinsamer
  Zoom/Scroll-Achse, darunter **zwei nebeneinander liegende Tabellen**
  (links Tag-Blöcke, rechts Prozesse — siehe unten).
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
- **Resize bestehender Tag-Blöcke:** Sobald **genau ein** geschlossener
  Tag-Block selektiert ist (und kein gezogener manueller Range aktiv ist),
  erscheinen am linken und rechten Rand des Blocks Resize-Greifer. Beim
  Ziehen:
  - Die Live-Position **clamp**t hart an die Kante des nächsten Nachbar-
    Tag-Blocks (links: dessen `end_time`, rechts: dessen `start_time`),
    fällt zurück auf die Tagesgrenzen `00:00`/`24:00`, wenn kein Nachbar
    in der jeweiligen Richtung existiert.
  - Die gezogene Kante snappt während des Drags auf das
    Granularitätsraster (Start floor, Ende ceil).
  - Der Block muss mindestens eine Granularitätsstufe (oder 1 s, falls
    `tag_block_granularity_min = 0`) breit bleiben — die gezogene Kante
    kann nicht über die andere Kante hinaus.
  - Beim Loslassen wird `Orchestrator.ResizeBlock` → `TagBlockRepo.Resize`
    aufgerufen. Das Repo prüft den Non-Overlap-Invariant erneut und
    aktualisiert `start_time`, `end_time` und `duration_sec` in **einer**
    Transaktion.
  - **Auto-Tag-Blöcke** werden bei Resize implizit auf `is_manual = 1`
    gehoben — der manuelle Eingriff hat Vorrang vor erneuter
    Auto-Tag-Reproduktion.
  - Offene Blöcke (Auto-Run, offener manueller Tag) sind nicht resizable
    — die Repo-Methode lehnt das ab.
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
- **Tabellen** unterhalb der Strips, in einem zweispaltigen Flex-Container
  (Verhältnis 40 / 60, immer nebeneinander — auch bei schmalen Fenstern;
  jede Spalte scrollt unabhängig mit `max-h: 65vh`):
  - **Linke Spalte — Tag-Block-Tabelle:** pro `tag_block` eine Zeile mit
    Zeitraum, Dauer, `manuell|auto`, Beschreibung, Tag-Chips (Parent + Sub).
  - **Rechte Spalte — Process-Track-Tabelle:** gruppiert aufeinanderfolgende
    Tracks mit gleichem Prozess. Der Pfeil expandiert die Gruppe und zeigt
    die einzelnen Fenstertitel. Bekommt 60 % der Breite, weil Fenstertitel
    deutlich länger sind als Tag-Chips.

### 2.3.1 Tätigkeitsbeschreibung pro Tag-Block
- Jeder `tag_block` hat ein optionales freies Textfeld `description`.
- Beim Anlegen einer manuellen Range („Tag-Button" im Auswahl-Panel) wird
  der Beschreibungstext mit übernommen.
- Bei einem **offenen** manuellen Tag (siehe §2.4.2) wird die beim Start
  hinterlegte Beschreibung auch nach einer Auto-Tag-Unterbrechung erneut
  in den fortgesetzten manuellen Block geschrieben.
- Beim Personio-Sync wird die Beschreibung an den aus Tag/Sub-Tag
  generierten Kommentar angehängt (Format: `"<tag-comment> — <description>"`).

### 2.3a Hilfe-Tab (eingebettetes Benutzerhandbuch)

- Hauptfenster hat einen Tab **Hilfe**, der das Benutzerhandbuch
  (`docs/user/*.md`) im linken-Sidebar-/rechte-Inhalt-Layout anzeigt.
- **Bereitstellung:** Markdown-Dateien sind in den Binary eingebettet
  via `//go:embed docs/user/*.md` (Datei `userdocs.go` am Modul-Root,
  da `//go:embed` keine `..`-Pfade erlaubt; identisches Muster wie
  `frontend.go`). Damit funktioniert die Hilfe offline und passt
  garantiert zur installierten Version.
- **Backend-Bindings:**
  - `App.ListUserDocs() ([]UserDocPage, error)` — liefert
    `{slug, title}` in der durch `helpPageOrder` festgelegten
    Sidebar-Reihenfolge. Titel wird aus dem ersten H1 der jeweiligen
    Markdown-Datei gelesen, Fallback ist der Slug.
  - `App.GetUserDoc(slug string) (string, error)` — liefert den rohen
    Markdown-Inhalt. Slug wird gegen `helpPageOrder` whitelisted
    (kein Pfad-Traversal).
  - `App.OpenHelpTab()` — vom Tray-Eintrag *Hilfe* aufgerufen; ruft
    `ShowWindow` und feuert das Wails-Event `help:open` an das
    Frontend, das daraufhin in den Tab *Hilfe* wechselt.
- **Frontend:** `react-markdown` + `remark-gfm` rendern die Datei in
  einer dunklen, mit Tailwind-Klassen ausgezeichneten Ansicht (kein
  `@tailwindcss/typography`, um die Dependency-Last gering zu halten).
  Interne `*.md`-Links (z. B. `[Tags](tags.md)`) werden abgefangen
  und navigieren in der Sidebar — sie öffnen keinen Browser.
- **Sidebar-Reihenfolge** (`helpPageOrder` in `internal/app/app.go`):
  `README`, `installation`, `einstellungen`, `zeiterfassung`, `tags`,
  `auto-tagging`, `personio`, `tray`, `quick-tag`. Eine neue Doku-Seite
  ist erst nach Eintrag in diese Liste sichtbar (bewusste
  Zwei-Schritt-Aktion: Datei droppen + Slug eintragen).

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
  - `description`: optionaler Freitext (max. 250 Zeichen). Wird beim
    Öffnen eines Auto-Tag-Blocks als `tag_blocks.description` übernommen
    und damit per §2.5.3 in den Personio-Comment angehängt. Whitespace-
    only wird als „keine Description" behandelt (`NULL` in der DB).
  - `priority`: Integer; höhere Priorität gewinnt bei Mehrfach-Match
  - `enabled`: bool (Default `true`). Deaktivierte Regeln werden im
    Matching ignoriert; Toggling ist im Regel-Listeneintrag direkt per
    Schalter möglich (UI feuert `UpdateRule` ohne Speichern-Klick).
- **Lebenszyklus:**
  1. Tracker meldet `OnFocusChanged(name, title, at)`.
  2. Orchestrator prüft Regeln in Priorität-DESC-Reihenfolge.
  3. Trifft eine Regel: ist bereits ein Auto-Block der **gleichen Regel**
     offen, wird er weitergeführt. Sonst wird ein offener Auto-Block der
     anderen Regel geschlossen (Floor-Snap auf Granularität, siehe §2.4.3)
     und ein neuer Auto-Block für die treffende Regel geöffnet (Floor-Snap
     auf Granularität, mit `description` aus der Regel falls gesetzt).
  4. Trifft keine Regel: ein offener Auto-Block wird geschlossen.
- **Auto-Description vs. manuelle Sitzung:** Bei einer Auto-Tag-
  Unterbrechung (§2.4.2) erhält der **Auto-Block** die Description aus
  der Regel; der pausierte manuelle Block behält seine ursprüngliche
  Description und nimmt sie beim Wiederanlauf (§2.4.2 Schritt 6) erneut
  in den fortgesetzten manuellen Block.
- **Regex-Engine:** Go-Standardbibliothek `regexp` (RE2-Syntax) — kein
  Backtracking, lineare Laufzeit. Ungültige Patterns werden beim Speichern
  via `regexp.Compile` validiert und abgelehnt.
- **Zero-Length-Suppression:** Floor-Snap am Anfang **und** am Ende kann
  einen 0-Sekunden-Auto-Block produzieren (wenn ein Match die Granularität
  unterschreitet). Solche Blöcke werden nicht persistiert / vor dem Close
  wieder gelöscht.
- **Vorrang manueller Range-Tags:** §2.4.3 beschreibt das Verhalten bei
  Überlappung — eine manuelle Range schneidet Auto-Blöcke aus.

### 2.4.1a Auto-Tagging aus Kommunikations-Prozessen (Override)
- Trifft eine Auto-Tag-Regel beim `OnCommunicationChanged`-Event auf eines
  der aktiven Kommunikations-Fenster (siehe §2.1a), öffnet der Orchestrator
  einen **Kommunikations-getriebenen Auto-Tag-Block** (`openCommAuto`).
  Dieser hat **absoluten Vorrang** vor jedem Fokus-getriebenen Auto-Tag im
  selben Zeitraum.
- **Konsequenzen für die State-Machine:**
  1. Eröffnung: vorhandener `openAuto` (Fokus) wird mit Reason
     `auto_overridden_by_comm` geschlossen; ein offener manueller Block
     wird mit `manual_paused_for_comm` pausiert (gleiche Semantik wie bei
     einer regulären Auto-Unterbrechung). Anschließend `startCommAuto` mit
     Granularitäts-Floor-Snap.
  2. Während `openCommAuto` aktiv ist, halten `OnFocusChanged` /
     `OnFocusCleared` lediglich `focusActive` und `focusedProcess` aktuell
     — sie öffnen / schließen **keine** Fokus-Auto-Tag-Blöcke.
  3. Schließung: das letzte passende Kommunikations-Fenster verschwindet
     (Reason `comm_window_gone`) oder eine andere Comm-Regel matcht jetzt
     (`comm_rule_switched`). Anschließend re-evaluiert der Orchestrator
     den letzten gemeldeten Fokus → öffnet ggf. einen Fokus-Auto-Tag bzw.
     setzt einen pausierten manuellen Block fort.
- **Mehrere Kandidaten:** Liefert die Tracker-Enumeration mehrere aktive
  Kommunikations-Fenster gleichzeitig (z. B. Teams + Zoom), gewinnt die
  Regel mit der höchsten Priorität (Priority DESC, Id ASC); die übrigen
  sind „aktiv, aber wirkungslos".
- **Granularitäts-Snapping:** Comm-Auto-Blöcke folgen denselben Regeln wie
  reguläre Auto-Blöcke (Floor am Start, Floor am Ende; Zero-Length-
  Suppression). `description` aus der Regel wird übernommen.
- **Logging-Reasons** (vgl. §5): `auto_overridden_by_comm`,
  `manual_paused_for_comm`, `comm_window_gone`, `comm_rule_switched`
  (jeweils mit `_zero_length`-Variante).

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
1. User trägt im **Einstellungen-Tab** seine **Tenant-Subdomain** ein (z. B. `acme`).
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
  `tag_blocks.description`. Die Block-Description kann aus einem
  manuellen Block, einer manuellen Range oder einer Auto-Tag-Regel mit
  `description`-Feld stammen. Innerhalb eines Laufs gehören alle Blöcke
  per Konstruktion zum selben Kommentar — Auto-Blöcke mit Rule-
  Description erzeugen damit eine eigene Period gegenüber Auto-Blöcken
  ohne Description (unterschiedlicher Comment-Schlüssel).

#### 2.5.4 Fehlerbehandlung
- Antwort `401`/`403` oder `30x → /login` ⇒ `ErrSessionExpired`. Im UI: rotes Banner mit Hinweis auf erneute Anmeldung; Tray-Sync schreibt nur ins Log.
- Antwort `4xx` (z. B. Tag nicht buchbar) ⇒ Eintrag im `Result.Errors` mit Datum + Statusmeldung.
- Auth-Header (`X-CSRF-Token`, Cookies) werden **nie** geloggt.

#### 2.5.5 Sync-Modi & Idempotenz
- Einzelner Tag (Timeline-Button + Tray-„Sync zu Personio (heute)").
- Zeitraum (`SyncRange`) — aktuell intern, im UI nicht exponiert.
- Automatisch beim Starten der Anwendung für den letzten unsynchronisierten
  Werktag vor heute (siehe §2.5.6).
- Personio's `PUT day` ist idempotent (ersetzt den Tag). Bereits
  synchronisierte Tag-Blöcke werden lokal mit `synced_at` und der `day_id`
  als `personio_id` markiert (`tag_blocks` trägt diese Felder). Erneuter
  Sync überschreibt den Personio-Tag mit dem aktuellen Stand; manuelle
  Änderungen in Personio gehen dabei verloren — solange der Anwender nicht
  vorher auf „Importieren" wählt (siehe §2.5.7).

#### 2.5.7 Pre-Sync-Check & Personio-Import

Bevor der eigentliche `PUT day` ausgeführt wird, ruft der Syncer
`Syncer.Preflight(ctx, day)` auf — eine reine Lese-Operation, die das
Timesheet abruft und die bestehenden **Work-Type-Perioden** des Tages
zurückgibt. Break-Perioden lösen den Check nicht aus, weil Personio sie
unabhängig von User-Eingaben nach Arbeitsrecht generiert.

**Wenn `Preflight` Work-Perioden meldet**, wird statt des direkten Pushs
ein Modal-Dialog angezeigt, der dem User drei Optionen gibt:

| Option | Wirkung |
| --- | --- |
| **Trotzdem überschreiben** | Klassisches `PUT day` — Personio-Stand wird durch den lokalen Hashpoint-Stand ersetzt. Manuelle Personio-Eingaben gehen verloren. |
| **Aus Personio importieren** | `Syncer.ImportDay(ctx, day)`: zieht die Personio-Perioden in die `tag_blocks`-Tabelle. Lokale Tag-Blöcke gewinnen — überlappende Importe werden zurechtgeschnitten (siehe unten). Anschließend wird **kein** PUT ausgeführt; der User kann reviewen und erneut auf Sync klicken. |
| **Abbrechen** | Schließt das Modal ohne Datenänderung. |

**Trim-Logik im Import:** Für jede Personio-Periode wird der Bereich
`[start, end)` mit den `[start_time, end_time)`-Intervallen aller bereits
geschlossenen lokalen Tag-Blöcke des Tages verschnitten (`subtractRanges`).
Übrig bleiben null bis mehrere disjunkte Sub-Bereiche, die als neue
manuelle Tag-Blöcke (`is_manual = 1`) eingefügt werden — frisch
importierte Blöcke werden nicht von der Auto-Tagging-Engine eingesammelt.
Sie tragen **kein** `synced_at` und werden bei einem späteren Sync ggf.
zurück nach Personio gepusht (sofern der gewählte Tag
`SyncToPersonio = 1` hat).

**Tag-Auflösung beim Import:**
- `personio_project_id` der Period → lokales Tag mit gleichem
  `personio_project_id`. Match-Vergleich nutzt String-Form
  (`strconv.FormatInt`).
- Kein Match → Fallback auf den Auto-Tag `#PersonioImport`. Der wird
  beim ersten Import angelegt (Top-Level, Farbe `#94a3b8`,
  `SyncToPersonio = 0`, damit re-importierte Blöcke nicht ungewollt
  zurück nach Personio kreiseln). `ensureFallbackTag` sucht den Tag
  per **Namens-Match** auf der Top-Level-Ebene — wird er gelöscht oder
  umbenannt, legt der nächste Fallback-Bedarf einen frischen
  `#PersonioImport` an. Eigenschaften wie Farbe oder Project-ID kann
  der User im Tag-Manager überschreiben, ohne dass der Lookup das
  bemerkt.

**Granularität:** Der Import nutzt **die Personio-Originalzeiten ohne
Snap auf das lokale Granularitätsraster**. Personio ist hier die Quelle
der Wahrheit; ein nachträgliches Snapping würde die Importe je nach
Nachbarschaft erneut beschneiden.

**Period-Typ:** Nur `type = "work"` wird importiert. `break` und andere
Typen werden gezählt (`PeriodsSkipped++`) aber nicht eingefügt.

**ImportResult-Felder:** `PeriodsConsidered` (alle non-trivialen Personio-
Perioden), `BlocksCreated` (effektiv eingefügte Tag-Blöcke), `PeriodsSkipped`
(durch Trim, Type oder Parse-Fehler übersprungen), `FallbackTagUsed` (true
sobald mindestens eine Period den `#PersonioImport`-Tag bekommen hat),
`Errors` (per-period Fehler beim Insert oder Parsen).

#### 2.5.6 Sync beim Starten („Startup-Sync")
Beim Start der Anwendung wird der **letzte Tag vor heute mit noch
unsynchronisierten Tag-Blöcken** automatisch an Personio übertragen. Das
ersetzt den früheren Shutdown-Sync, der bei System-Shutdowns regelmäßig
fehlschlug, weil Windows das Netzwerk vor dem laufenden Sync-Request
abreißt.

**Auslöser:** Wails-`OnStartup` ruft `App.runStartupSync(ctx)` als
Goroutine auf, sobald das Frontend bereit ist. Der eigentliche Sync läuft
**asynchron** im Hintergrund — das Hauptfenster erscheint sofort.

**Tagesauswahl:**
1. Cutoff = heute, lokal, 00:00 (in `time.Local`).
2. `TagBlockRepo.LatestUnsyncedDayBefore(cutoff, loc)` liefert den
   neuesten Kalendertag mit mindestens einem geschlossenen Tag-Block,
   dessen `synced_at IS NULL` ist und dessen `start_time < cutoff`.
3. Liefert die Query keinen Tag (alles vor heute ist gesynct, oder es
   gibt keine Tag-Blöcke), läuft kein Sync — auch kein Banner.
4. Der gewählte Tag deckt damit auch Wochenenden/Urlaub ab: nach drei
   Tagen Abwesenheit wird der letzte Arbeitstag gesynct, nicht das
   leere „gestern".

**Ablauf:**
1. Personio-Session laden. Fehlt sie, läuft der Sync nicht und es wird
   **stillschweigend** kein Banner gezeigt (Info-Log).
2. `Syncer.Preflight(ctx, day)` als reine Lese-Operation. Findet die
   Vorabprüfung Work-Perioden auf dem Tag (siehe §2.5.7), wird statt des
   PUT das Wails-Event `startup-sync:conflict` mit der `SyncPreflight`-
   Payload gefeuert — das Frontend zeigt dasselbe Override/Import-Modal
   wie beim manuellen Sync. Der eigentliche Push wird in dem Fall vom
   User getriggert (oder vom „Abbrechen"-Knopf abgelehnt).
3. Sind keine Work-Perioden vorhanden, läuft `Syncer.SyncRange(ctx, day,
   day+24h)` mit hartem Timeout von **30 s** durch.
4. Ergebnis als Wails-Event `startup-sync:result` mit Payload
   (`status`, `day`, `periods`, `blocks_processed`, `blocks_skipped`,
   `errors`, `error_message`) ans Frontend feuern. Mögliche `status`-Werte:
   - `ok` — Sync erfolgreich, mindestens eine Periode geschrieben.
   - `partial` — Sync lief, einzelne Tage haben `Result.Errors`.
   - `failed` — harter Fehler (Session abgelaufen, Netz, Timeout).
   - `skipped` (intern; aktuell nicht emittiert) — alle unsynchronisierten
     Blöcke des Tages waren `should-skip` (gelöschtes Tag,
     `sync_to_personio = 0`); kein Banner.

**Frontend-Banner:** `App.tsx` lauscht auf `startup-sync:result` und zeigt
einen dismissbaren Banner über dem Hauptbereich (grün/rot/amber je nach
Status). Der Banner ist tab-übergreifend sichtbar.

**Nicht abgedeckt:** Hat die Anwendung beim Beenden einen Block ohne
`end_time` hinterlassen (z. B. Stromausfall), schließt der Orchestrator
beim Start dangling Blöcke per `Recover()` und
`CloseDanglingManualAtStartup` — der nachfolgende Startup-Sync
übernimmt die nun geschlossenen Blöcke automatisch.

**Idempotenz:** Personio's `PUT day` überschreibt den Tag. Ein zweiter
manueller Sync für denselben Tag ist gefahrlos. Da der Startup-Sync nur
Tage anfasst, die mindestens einen Block ohne `synced_at` haben,
verursacht ein erneuter Anwendungsstart am selben Tag keinen redundanten
Sync.

---

## 2.6 Rufbereitschaft & Off-Hours-Erweiterung (Plugin)

Hashpoint erkennt automatisch Tag-Blöcke, die **außerhalb der
Arbeitszeit** an einem als „On-Call" markierten Tag liegen, und legt für
jeden eine Doc-Zeile in der Rufbereitschafts-Inbox an. Der User füllt
dort ein Formular (Anwendung, Incident-Typ — *planned_maintenance* |
*service_disruption* —, Lösung) und überträgt die Doc an alle laufenden
Plugins, die die Capability `oncall_documentation` ausspielen. Hashpoint
pusht selbst nirgendwohin; die Distribution lebt komplett in Plugins.
SDK-Vertrag siehe `docs/plugins/`.

### 2.6.1 Qualifikation eines Tag-Blocks

Eine Doc-Zeile entsteht, sobald **alle** drei Bedingungen zutreffen
(Implementierung: `internal/plugin/oncall/qualifies.go`):

1. Der Block ist geschlossen (`end_time != NULL`).
2. Sein lokal-zeitliches Intervall `[start_time, end_time)` schneidet die
   berechnete **Off-Hours-Timeline**.
3. Sein Tag (oder ein Ancestor in der Tag-Hierarchie) ist in
   `config.oncall.tag_ids` aufgelistet.

Die Off-Hours-Timeline kommt grundlegend aus `WorkScheduleConfig`
(`work_days`, `start_hour..end_hour` lokal) und kann von Plugins
erweitert oder eingeschränkt werden (§2.6.2). Nach jeder Block-Mutation
(Close, Re-Tag, Resize, Range-Create) läuft `Recheck`: neuer Match ⇒
neue Doc im Status `draft`; verlorene Qualifikation ⇒ bestehende Doc
wird `stale` markiert (nie automatisch gelöscht — der User verwirft sie
aus der Inbox). Bereits eingereichte Docs (`submitted`, `partial`,
`failed`) sind historisch und werden vom Recheck nicht angefasst.

### 2.6.2 Plugin-Capability `off_hours_provider`

Plugins können der Off-Hours-Timeline beliebige `[start, end)`-Intervalle
hinzufügen oder vorhandene Intervalle wieder rausnehmen. Use-Cases:
dynamische Feiertage, regionale Brückentage, Betriebsruhe-Fenster — oder
die umgekehrte Richtung („geplante Sonderschicht am Samstag ist *kein*
On-Call").

Jedes vom Plugin gelieferte Intervall trägt ein `Kind`:

| `Kind`   | Wirkung                                                       |
|----------|---------------------------------------------------------------|
| `add`    | Range wird zu off-hours (Default, wenn das Feld leer ist).    |
| `remove` | Range wird zu working-hours (übersteuert `work_schedule`).    |

Berechnung pro Block-Recheck:

1. Basis: Off-Hours aus `WorkScheduleConfig`.
2. `add`-Intervalle aller laufenden Provider-Plugins werden vereinigt
   (Union).
3. `remove`-Intervalle aller Plugins werden anschließend subtrahiert —
   **`remove` gewinnt global**, auch gegenüber `work_schedule` und gegen
   `add`s anderer Plugins.

**Aufrufmodell:** Host fragt pull-basiert beim Recheck ab. Antworten
werden pro Plugin in einem Year-Bucket im Arbeitsspeicher gecached;
Cache-Invalidierung bei Plugin-Reload, -Stop oder -Crash. **Kein
DB-Cache, kein Backfill** — gestoppte Plugins liefern keine Off-Hours,
und ein frisch installiertes/aktiviertes Plugin wirkt nur auf ab jetzt
mutierte Blöcke. Bestehende Doc-Zeilen werden vom Plugin-Lifecycle
nicht angefasst.

SDK-Vertrag, Wire-Format und Author-Skeleton:
[`docs/plugins/capability-off-hours-provider.md`](plugins/capability-off-hours-provider.md).

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
  description  TEXT,                  -- optional, max 250 Zeichen; wird auf Auto-Blöcke übernommen
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
tenant = "acme"             # Subdomain, der Login läuft via CDP — keine API-Tokens

[communication]
process_names         = ["teams.exe"]   # parallel zum Fokus erfasst, sobald ein
                                        # sichtbares Top-Level-Fenster eines
                                        # Eintrags läuft. Auto-Tag-Regeln, die
                                        # hier matchen, übersteuern Fokus-Auto-
                                        # Tags (§2.1a, §2.4.1a).
title_exclude_phrases = []              # Substrings (case-insensitive). Enthält
                                        # der Fenstertitel eines Comm-Fensters
                                        # einen dieser Strings, wird das Fenster
                                        # **nicht** als Comm-Fenster behandelt
                                        # — weder Comm-Track noch Comm-Auto-
                                        # Tag-Override. Hot-reloadable, in
                                        # jedem Tick neu evaluiert (§2.1a).

[work_schedule]
start_hour = 8                          # inklusiv, lokale Zeit, [0..23]
end_hour   = 18                         # exklusiv, lokale Zeit, [1..24]
work_days  = ["Mon","Tue","Wed","Thu","Fri"]   # Mo–So Kurzformen; bestimmt
                                               # zusammen mit start/end_hour
                                               # die Off-Hours-Timeline (§2.6.1).
                                               # Plugins können die Timeline
                                               # via off_hours_provider
                                               # erweitern oder einschränken
                                               # (§2.6.2).

[oncall]
tag_ids = []                            # IDs der Tags, die als
                                        # Rufbereitschafts-Tags gelten. Sub-
                                        # Tags qualifizieren über die Tag-
                                        # Hierarchie automatisch mit. Leere
                                        # Liste ⇒ Feature dormant (§2.6).
```

> Auth-Daten (Personio-Cookies, XSRF-Token) liegen **nicht** in `config.toml`,
> sondern verschlüsselt im Windows Credential Manager unter
> `TimeTracker.PersonioSession`.

---

## 5. Build & Deployment
- Single-File-Executable: `go build -ldflags="-H windowsgui"` (kein Konsolenfenster).
- Icon-Embedding via `tc-hib/go-winres` (`.syso` mit Icon, Manifest und
  VERSIONINFO; `TimeDateStamp=0` für reproduzierbare Builds).

### 5.1 Release-Pipeline (`.github/workflows/release.yml`)

Push auf `main` → Auto-Tag (Semver, Patch-Bump per Default) → Build →
GitHub-Release mit folgenden Artefakten:

- `hashpoint.exe` — Single-File-Build, GUI-Subsystem, reproduzierbar
  (pinned Go/Node, `-trimpath`, `-buildid=`, commit-derived `buildDate`).
- `hashpoint-<version>.msi` — WiX-3.14-Installer (siehe §5.2).
- `checksums.txt` — SHA-256 für beide Artefakte.

`[skip release]` oder `[skip ci]` im Commit-Message überspringt den
Tag-Bump und damit den Build.

### 5.2 MSI-Installer (WiX 3.14)

- Quelle: `build/wix/hashpoint.wxs`. Sprache: `1031` (de-DE).
- Build-Toolchain: WiX Toolset 3.14.x. Auf den `windows-2022`-Runnern
  ist eine aktuelle 3.14.x bereits installiert; der Workflow erkennt
  sie über die `WIX`-Env-Var bzw. das `Program Files (x86)\WiX Toolset
  v3*\bin`-Verzeichnis und fällt nur dann auf `choco install
  wixtoolset` zurück, wenn die Toolchain in einem zukünftigen
  Image-Wechsel verschwindet. `candle` kompiliert
  `.wxs` → `.wixobj`, `light` linkt das `.wixobj` → `.msi`. Quellpfade
  (`hashpoint.exe`, Icon, Version) werden via `-d`-Preprocessor
  übergeben, damit die `.wxs` repo-relativ bleibt.
- **Install-Scope:** `perMachine`. Default-Pfad
  `%ProgramFiles%\Hashpoint\hashpoint.exe`. UAC-Prompt erforderlich;
  `msiexec /i hashpoint-<version>.msi /quiet` für IT-Roll-Outs.
- **Stable Identifiers** (dürfen niemals neu generiert werden, sonst
  bricht die `MajorUpgrade`-Kette):
  - `UpgradeCode`: `8B0A3C7E-7D1F-4B23-9A2C-1F8E5D9C0B42`
  - `MainExecutable` GUID: `4D2E9F71-2C3B-4A5E-91F2-7E4D8B1C3A60`
  - `StartMenuShortcut` GUID: `6F5C4E81-3B7A-4D9C-B0E2-8A5F2D7C9E31`
  - `AutostartHKCU` GUID: `9A1B2C3D-4E5F-46A7-B8C9-D0E1F2A3B4C5`
  - `ProductId="*"` → bei jedem Upgrade neu generiert; `MajorUpgrade`
    sorgt dafür, dass die Vorgänger-Version vor der Installation
    deinstalliert wird.
- **Komponenten:**
  1. `MainExecutable` — schreibt `hashpoint.exe` nach `INSTALLDIR`
     (Win64, KeyPath, Checksum).
  2. `StartMenuShortcut` — Eintrag „Hashpoint TimeTracker" im
     Startmenü; KeyPath ist ein HKCU-Marker (`Software\dusthoff\Hashpoint`),
     damit die Komponente per-User installierbar bleibt.
  3. `AutostartHKCU` — schreibt `HKCU\Software\Microsoft\Windows\
     CurrentVersion\Run\HashpointTimeTracker = "<install-pfad>\hashpoint.exe"`
     für den **installierenden User**. Der Autostart wird ausschließlich
     hier gesetzt; die Anwendung enthält keinen In-App-Toggle mehr. Andere
     User des Systems aktivieren den Autostart bei Bedarf manuell, indem
     sie denselben HKCU-Run-Eintrag für ihr eigenes Profil anlegen.
- **ICE57 unterdrückt** (`light -sice:ICE57`): WiX warnt sonst, dass
  HKCU-Registry in einem per-machine-Install nicht „sauber" ist; wir
  akzeptieren den Trade-off bewusst (siehe Kommentar in
  `release.yml` und `hashpoint.wxs`).
- **Versionierung:** Git-Tag `vX.Y.Z` → MSI-`ProductVersion=X.Y.Z`. Tag
  ohne kompatibles Format (Pre-Release-Suffix etc.) bricht den Build
  vor dem MSI-Schritt ab.

---

## 6. Roadmap / Phasen für die Umsetzung
1. **Phase 1 – Tracking-Core:** Windows-API-Wrapper, Tracking-Loop, SQLite-Persistenz, Logging.
2. **Phase 2 – Tray-App:** systray-Integration, Pause/Resume.
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
# ADR 0001 – Trennung von Collector- und UI-Prozess

- **Status:** Akzeptiert — in Umsetzung (ab 2026-05-21)
- **Datum:** 2026-05-20
- **Bezug:** Issue #21 (stiller Tod nach Inaktivität), PR #22 (Diagnose/Resilienz), Trade-off-Matrix Option **B** im #21-Kommentar
- **Autor:** Analyse im Vorfeld einer Entscheidung

---

## Update 2026-05-21 — Entscheidung & Diagnose-Bestätigung

**Diagnose abgeschlossen.** Geräte-/Flotten-Analyse über 7,5 Monate: **kein** nativer WebView2-Crash (kein WER-`AppCrash`/Event 1000 für `hashpoint.exe`/`msedgewebview2.exe`) und **kein** BSOD/Speicherdruck-Kill (kein Resource-Exhaustion 2004). Übrig bleibt ein **stiller Teardown rund um den Modern-Standby-/Hibernate-Zyklus**; der endgültige Exit-Code-Beweis (Security 4688/4689) fehlt nur, weil er nicht im Intune-Diagnosepaket gesammelt wird.

**Konsequenz für die Optionswahl:** Die *Crash-Isolation* (ursprünglich beworbener Kernnutzen von B) ist damit empirisch gegenstandslos. B wird dennoch gewählt — wegen des **Survivability**-Teils (§5): ein headless Collector ohne Edge-Host wird vom Modern-Standby-Teardown nicht miterfasst. **Option A (Heartbeat) ist bereits umgesetzt** (Commit `3f35e18`) und kappt den Datenverlust unabhängig vom Todesmodus.

**Festgelegte Entscheidungen:**

- **Transport (§7.3):** **Named Pipe + gRPC.** gRPC liefert typsichere, bidirektionale Streams für den Event-Kanal; der Stub-Code wird per `buf` generiert und **eingecheckt**, sodass der Laufzeit-Build pure-Go bleibt (kein System-`protoc`). Bewusst abweichend von der protoc-freien Linie des Plugin-SDK (`docs/plugins/protocol.md`).
- **Packaging:** **ein Binary, zwei Modi** (`--collector` / `--ui`); der Collector spawnt sich selbst als `--ui`. Eine Version, ein Handshake, minimale Installer-Änderung.
- **Quit-Semantik (§7.2):** Tray-„Beenden" beendet Collector **und** UI; das Fenster-X schließt nur die UI, der Collector läuft weiter.
- **Quick-Tag-Cold-Start (§7.1):** für v1 akzeptiert (Hotkey startet den UI-Prozess kalt).
- **Migration (§7.4):** collector-eigener Single-Instance-Name; Ablösung des Alt-Monolithen in der Install-Phase.

---

## 1. Kontext

Hashpoint verschwindet nach längerer Inaktivität still aus dem Tray (Issue #21). Die Diagnose-Telemetrie aus PR #22 belegt im Folge-Log (neuer Build ab 19.05.):

- `wails.Run` kehrt beim realen Tod **nicht** zurück → der Fallback-Cleanup-Block (`cmd/timetracker/main.go:335`) wird nie erreicht.
- Der Power-Monitor feuert **nicht** (kein `power: suspend notification received`), obwohl ein Lauf nachweislich in Sperre/Standby lief.
- Recovery klemmt offene Tracks weiter auf `start + IdleThreshold` (5 min) → Datenverlust unverändert.

Der Prozess wird also ohne jeden Userspace-Callback beendet — entweder ein **harter OS-Kill** (Modern Standby / Speicherdruck) oder ein **nativer Crash im WebView2-Layer**. In beiden Fällen können graceful-cleanup-Maßnahmen konstruktionsbedingt nicht greifen.

### Architekturursache

Hashpoint ist heute eine GUI-/WebView2-App, die per verstecktem Fenster (`HideWindowOnClose: true`) einen Dauer-Background-Agent mimt. Tracker, DB, Sync, Plugin-Host und Tray hängen am Leben **dieses einen** Prozesses — dessen fragilster Teil der WebView2/Edge-Host ist. Der Collector teilt sein Schicksal mit einem GUI-Host, den er gar nicht braucht.

### Günstige Ausgangslage im Code

Zwei vorhandene Eigenschaften entschärfen einen Split erheblich:

1. **Multi-Prozess-IPC ist bereits etabliert.** `internal/plugin/host.go` nutzt HashiCorp `go-plugin` (Subprozesse, net/rpc-Transport via `ProtocolNetRPC`, Handshake, Lifecycle, Versions-Disziplin via `sdk.HostAPIVersion`). IPC und Subprozess-Verwaltung sind im Projekt keine neue Forschung. **Achtung Lifetime-Modell:** Der Host *spawnt* die Plugins (`Cmd: exec.Command`), *besitzt* und *killt* sie (`client.Kill()`); ein Plugin überlebt seinen Host nicht. Für den Collector ist dieses Modell daher **invertiert** zu nutzen (siehe §2).
2. **Die UI↔Backend-Schnittstelle läuft durch zwei schmale Engstellen.** Go-seitig ist `app.App` laut Package-Doc *„the single bridge between the JS layer and the Go backend; no other package speaks to Wails directly."* TS-seitig geht alles durch `frontend/src/api/index.ts` + `EventsOn` (CLAUDE.md §9). Ein Split ändert damit nur, **was hinter der Fassade liegt**, nicht die Fassade.

---

## 2. Entscheidung (vorgeschlagen)

Hashpoint wird in **zwei Prozesse** geteilt:

- **Collector** – langlebig, headless, **kein WebView2**. Besitzt die gesamte Domäne: SQLite (alleiniger Owner), Tracker, Orchestrator, Personio-Session/Syncer, Plugin-Host, globaler Quick-Tag-Hotkey, **Tray-Icon**, Config-Owner inkl. Live-Reconfig. Wird beim Login automatisch gestartet, überlebt das Schließen der UI und ist **Owner des UI-Prozesses**.
- **UI-Prozess** – wegwerfbare Wails-Shell. **Der Collector spawnt sie als Kindprozess on demand** (Tray „Öffnen" / Hotkey), **besitzt ihren Lifecycle**, beendet sie beim Schließen und respawnt sie nach einem Crash. Sie rendert das Frontend, steuert **nur ihr eigenes Fenster** und hält einen Client zur Collector-API. **Kein eigener UI-Autostart** — die Existenz der UI hängt ausschließlich am Collector.

Die UI behält ihre Wails-Bindings, aber als **dünne Proxys**: jede Methode reicht über eine lokale API zum Collector weiter. Hält man die Binding-Namen identisch, braucht das **Frontend praktisch keine Änderung**.

```
┌─ Collector (langlebig, headless, KEIN WebView2) ──────┐
│  SQLite (alleiniger Owner) · Tracker · Orchestrator   │
│  Sessions · Syncer · Plugin-Host · Hotkey · Tray-Icon │
│  Config-Owner + OnConfigSet-Live-Reconfig             │
│            ▲   lokale API (req/resp + Event-Stream)   │
└────────────┼──────────────────────────────────────────┘
             │  Named Pipe (user-ACL) ODER 127.0.0.1+Token — Collector spawnt & killt die UI
┌────────────┼──────────────────────────────────────────┐
│  UI-Prozess (wegwerfbar, on demand) — Wails-Shell      │
│  Frontend (unverändert) · Fenstersteuerung (lokal)     │
│  App-Proxy: RPC → API · Events → wailsruntime.Emit     │
└────────────────────────────────────────────────────────┘
```

### Transport

- **Bevorzugt:** Windows **Named Pipe** mit user-beschränkter ACL — Windows-nativ, keine Port-Vergabe, keine Erreichbarkeit durch andere User.
- **Alternativ:** `127.0.0.1:<port>` + per-Session-Token (Datei mit user-ACL im `%LOCALAPPDATA%`).
- **Event-Stream** Collector→UI: gRPC-Server-Stream, WebSocket oder SSE — je nach gewähltem RPC-Stil.
- **RPC-Stil:** Da Argumente/Returns heute schon JSON-serialisiert über Wails laufen, ist **HTTP+JSON + SSE** pragmatisch. **gRPC** wäre über `go-plugin` als Protokoll-Option verfügbar (Dependency vorhanden, aktuell aber net/rpc konfiguriert) und kostet ein großes `.proto` für ~69 Methoden.

### Verhältnis zu go-plugin

Die **Owner-Richtung ist bewusst gewählt**: der Collector ist der langlebige Elternteil und spawnt/besitzt die UI als Kindprozess. Der Collector darf **nie** umgekehrt ein Child/Plugin des Wails-Prozesses sein — sonst stürbe er mit dem fragilen WebView2-Host (das ursprüngliche Problem aus #21). Das verbietet auch die Abkürzung „spawne nur den Tracker als go-plugin-Child von Wails" — der stürbe mit dem Wails-Parent.

Die collector↔UI-Verbindung wird als **schlichter Kindprozess + Named Pipe** realisiert (Collector = API-Server, natürliche Richtung UI → Collector) — **nicht** über go-plugins Plugin-Hülle. Owner-Beziehung und Crash-Isolation (der Collector erkennt den Kind-Exit und respawnt) ergeben sich dabei ohne go-plugin. go-plugin bleibt unverändert der Host-Mechanismus für die **headless Leaf-Plugins** (`personio-dayoff`, `soggl`, …), die der Collector besitzt. Warum die UI **kein** go-plugin-Plugin wird, ist in §8 als geprüfte, verworfene Alternative dokumentiert.

---

## 3. Was wandert wohin

| Heute in einem Prozess | Nach dem Split |
|---|---|
| SQLite, Tracker, Orchestrator, Sessions, Syncer, Plugin-Host | → **Collector**, hinter der API |
| Globaler Quick-Tag-Hotkey (`FireQuickTag`) | → **Collector**; Tastendruck = Push-Event an UI |
| **Tray-Icon** (`cmd/timetracker/tray_windows.go`) | → **Collector** — überlebt UI-Schließen, braucht kein WebView2 (systray = eigenes Message-Window). Heilt das „Tray verschwindet"-Symptom direkt |
| Tray-Domänenaktionen (Pause, Sync, Manual-Tag, ListTags) | → in-process im Collector |
| Tray „Öffnen" (`ShowWindow`) | → **UI-Prozess starten/fokussieren** statt eigenes Fenster zeigen |
| `WindowShow/Hide/SetSize/SetPosition/AlwaysOnTop/Center` | → bleiben **UI-lokal** |
| `EventsEmit(a.ctx, …)` (≈9 Stellen) | → über API-Event-Stream pushen, UI re-emittet an JS |
| Config-Owner + `OnConfigSet`-Live-Reconfig (`main.go`) | → **Collector** |
| Personio-CDP-Login (`chromedp`) | → **Collector** (startet eigenen Browser, braucht keine UI) |

---

## 4. Sicherheit

Heute ist alles in-process; ein Split **schafft eine neue lokale Angriffsfläche**:

- Die API exponiert Tracking-Daten **und** Session-Steuerung (Personio-Login/-Logout, Plugin-Secrets, Entra-Login). Die Personio-Session liegt im Windows Credential Manager (`NewWinCredSessionStore`) — **nur der Collector** darf sie berühren.
- → **Named Pipe mit user-beschränkter ACL** ist die sensibelste Designentscheidung. Bei Loopback-TCP zwingend per-Session-Token, da `127.0.0.1` für jeden lokalen User erreichbar ist.
- Secret-tragende Methoden (siehe Anhang A, mit 🔒 markiert) bestätigen die Notwendigkeit eines authentifizierten Kanals — sie dürfen nicht über einen offenen Loopback-Port laufen.

---

## 5. Konsequenzen

**Positiv**
- **WebView2-Crash-Klasse eliminiert:** Ein Crash trifft nur die wegwerfbare UI; der Collector (Owner ihres Lifecycles) trackt weiter, hält das Tray und respawnt die UI beim nächsten Öffnen.
- **Modern-Standby-Kill-Klasse reduziert:** Ein headless Prozess ist überlebensfähiger (DAM-Freeze statt Kill, kein Edge-Host zum Einsammeln) — aber **nicht garantiert** überlebend.
- **Multi-Instanz-DB-Race verschwunden:** nur der Collector öffnet SQLite; die UI greift nie direkt zu.
- Composability mit Option **C**: der Scheduled-Task-Supervisor überwacht künftig nur noch den schlanken Collector statt einer WebView2-App.

**Negativ / Aufwand**
- API-Vertrag für ~69 Methoden + Event-Stream — pflegeintensiv.
- Event-Emission-Refactor: `EventsEmit`-Stellen von „Wails-Event" auf „API-Publish" umstellen.
- Lifecycle-Komplexität: **zwei** Single-Instance-Locks; Collector-Autostart; UI-Cold-Start beim Hotkey (siehe §7); Versions-Handshake zweier separat aktualisierbarer Binaries.
- Installer/MSI: zwei Binaries ausliefern + registrieren, Collector autostarten, Plugin-Seeding auf den Collector zeigen lassen.

---

## 6. Bezug zu den anderen Optionen

Dieses ADR ist **Option B** der Trade-off-Matrix und ersetzt die anderen nicht:

- **A (Heartbeat-Persistenz)** bleibt die billige Versicherung für jeden Resttod (Verlust = ein Poll-Intervall statt Stunden) — **sollte zuerst und unabhängig** umgesetzt werden (Design: §9).
- **C (Scheduled-Task-Supervisor)** supervisiert nach dem Split nur noch den Collector.
- Empfehlung bleibt gestaffelt: **A sofort**, dann je nach Kill-vs-Crash-Diagnose **C** (OS-Kill) oder **B** (WebView2-Crash). **A + B** = nachhaltige Lösung.

---

## 7. Offene Fragen

1. **Quick-Tag-Cold-Start:** Der Hotkey lebt im Collector, das Picker-Fenster im UI-Prozess. Ist die UI geschlossen (Normalfall), muss der Tastendruck den UI-Prozess **kalt starten** → Latenz. Akzeptabel, oder braucht es einen vorgewärmten UI-Prozess / ein separates leichtes Picker-Fenster?
2. **„Beenden"-Semantik:** Beendet der Tray-„Beenden" nur die UI oder auch den Collector? Vorschlag: beendet beide; reines UI-Schließen über das Fenster-X (`HideWindowOnClose`-Äquivalent) lässt den Collector laufen.
3. **Transport final:** Named Pipe + gRPC vs. Named Pipe + HTTP/JSON vs. Loopback+Token. Entscheidung vor Implementierung dokumentieren.
4. **Migration bestehender Installs:** Ein laufender Alt-Build (eine Instanz) muss beim Update sauber durch das Zwei-Prozess-Modell ersetzt werden (Single-Instance-Locks dürfen sich nicht gegenseitig blockieren).

---

## 8. Alternativen

- **UI als go-plugin-Plugin (Collector als Plugin-Host der UI) — geprüft, verworfen.** Die Owner-Richtung (Collector = Elternteil) wäre damit zwar korrekt, aber drei Gründe sprechen dagegen:
  1. **Verkehrsrichtung falsch.** go-plugins Hauptpfad ist Host → Plugin. Der dominante Verkehr ist hier UI → Collector (die 69 Methoden, Anhang A); er liefe über den sekundären Callback-/Broker-Kanal (`core.Init(ctx, api)`-Muster) — go-plugin „gegen den Strich".
  2. **GUI ≠ headless Plugin.** go-plugin beansprucht `stdin` (EOF → `os.Exit`), `stdout` (Handshake), `stderr` und Signal-Handling; `plugin.Serve` blockiert. Wails will Main-Thread, Win32-Message-Loop und eigenes Lifecycle. Koexistenz (`plugin.Serve` auf Goroutine, `wails.Run` auf Main) ist undokumentiert und brüchig — hohes Integrationsrisiko.
  3. **Kein Mehrwert.** Owner-Beziehung + Crash-Isolation liefert bereits der schlichte Kindprozess + Named Pipe: der Collector erkennt den Kind-Exit (analog zum heutigen `watchExit`) und respawnt beim nächsten Öffnen. go-plugin fügte nur Zwänge hinzu.

  → Gewählt: **plain Kindprozess + Named Pipe** (§2). go-plugin bleibt für die headless Leaf-Plugins.
- **Windows-Dienst (+ Session-Helper):** Würde überleben, aber Session-0-Isolation verbietet `GetForegroundWindow` der User-Session → man braucht ohnehin einen per-Session-Helper und landet bei diesem Modell mit zusätzlichem Dienst-/Rechte-Ballast. Verworfen (schlechteres Kosten/Nutzen).
- **Nichts tun / nur A+C:** Pragmatische Minimallösung ohne Architektur-Umbau, lässt aber die WebView2-Crash-Klasse bestehen.

---

## 9. Begleitmaßnahme: Heartbeat-Persistenz (Option A)

Unabhängig vom Split, aber hier mitdokumentiert, weil sie im Collector lebt (Tracker + DB-Owner) und die **Reihenfolge** vorgibt: **A vor B.** A ist der kleinste Eingriff mit dem größten Sofortnutzen gegen Datenverlust — und wirkt, *egal ob* der Prozess überlebt. Heute (Monolith) sofort einsetzbar, nach dem Split unverändert.

### Problem

`recover()` (`internal/tracker/tracker.go:332`) schließt offene Tracks beim nächsten Start auf `start + IdleThreshold` (5 min), geklemmt auf `now` bzw. den Start des nächsten offenen Tracks. Reale Aktivität zwischen dieser 5-Minuten-Klemmung und dem tatsächlichen Tod geht verloren — im Produktiv-Log bis zu mehreren Stunden (`recovered_end = start+5min`).

### Idee

Während ein Track offen ist, persistiert der Poll-Loop bei jedem Tick (= `PollInterval`, 2 s) einen `last_seen`-Zeitstempel. Recovery klemmt das Ende auf `last_seen` statt auf `start + IdleThreshold`. Der maximale Verlust sinkt von Stunden auf **ein Poll-Intervall**.

### Konkret

- **Schema:** neue **nullbare** Spalte `last_seen` (UTC) auf der Process-Track-Tabelle (analog für offene Communication-Tracks). Up- **und** Down-Migration in `/migrations` (CLAUDE.md §13). Nullbar → vor der Migration offene Zeilen fallen sauber auf die bisherige Heuristik zurück.
- **Schreiben:** im Tracking-Loop pro Tick, in dem der Track als aktiv bestätigt ist (nicht idle, nicht pausiert), `last_seen = now`. Über ein **Prepared Statement** (CLAUDE.md §4: „Prepared Statements für wiederholte Queries (Tracking-Loop)"). `last_seen` wird nur **monoton vorwärts** bewegt.
- **Recovery:** `end := last_seen`, falls gesetzt, sonst Fallback `start + IdleThreshold`; weiterhin auf `now` und auf den Start des nächsten offenen Tracks klemmen (Chaining-Invariante bleibt erhalten).
- **Keine neue Config:** Heartbeat-Takt = Poll-Takt → **kein** neues Feld in `config.go`, also keine Settings-UI-Pflicht (CLAUDE.md §9a unberührt).

### Kosten / Risiko

- Ein zusätzliches `UPDATE` pro Tick auf genau die offene Track-Zeile. Lokales SQLite, vernachlässigbar; Schema und Loop sind für solche Writes ohnehin vorgesehen.
- Kein Korrektheitsrisiko, solange `last_seen` strikt monoton vorwärts läuft (nie zurückdatieren).

### Definition of Done (CLAUDE.md §14)

- Migration up+down; Godoc auf der neuen Repo-Methode (z. B. `Touch(ctx, id, ts)`); table-driven Tests für die Recovery-Klemmung (`last_seen` gesetzt / nil-Fallback / auf next-start geklemmt) via In-Memory-SQLite.

---

## Anhang A – API-Schnitt: Methoden-Inventar nach Domäne

**69** der 78 exportierten `app.App`-Methoden sind Frontend-RPC (Quelle: `frontend/src/api/index.ts`) und queren die Prozessgrenze. **9** sind Lifecycle/Fenster/Hotkey-Handler mit Sonderbehandlung. Legende: 🔒 = secret-/session-tragend (authentifizierter Kanal zwingend), ⏳ = langlaufend (Netz/Browser, async + Timeout erwägen), ⚡ = latenzsensibel.

### RPC-Gruppen (Collector-API)

**Read-Models / Timeline** ⚡ *(häufig beim Tab-Öffnen)*
`ProcessTracksByDay` · `ProcessTracksBetween` · `TagBlocksByDay` · `TagBlocksBetween`

**Tag-Blocks (Mutationen)**
`CreateManualTagRange` · `ResizeTagBlock` · `SetTagBlockDescription` · `SetTagBlockTag` · `DeleteTagBlock` · `DeleteTagBlocks`

**Tags (CRUD)**
`ListTags` · `CreateTag` · `UpdateTag` · `DeleteTag`

**Rules (CRUD + Test)**
`ListRules` · `CreateRule` · `UpdateRule` · `DeleteRule` · `TestRule`

**Tracking-Steuerung** *(auch vom Tray genutzt)*
`PauseTracking` · `ResumeTracking` · `IsTrackingPaused`

**Manuelles Tagging** *(auch vom Tray genutzt)*
`StartManualTag` · `StopManualTag` · `IsManualTagActive`

**Quick-Tag-Picker** ⚡
`QuickTagSlots` · `QuickTagSelect` · `QuickTagDismiss`

**Sync (Personio)** ⏳
`SyncDay` · `SyncRange` · `PreflightSyncDay` · `ImportPersonioDay`

**Config**
`GetConfig` · `SaveConfig` 🔒 *(Config-Reconfig wirkt Collector-seitig)*

**Personio-Session** 🔒 ⏳
`PersonioStatus` · `PersonioCheck` · `PersonioLogin` *(chromedp)* · `PersonioLogout`

**Entra ID** 🔒
`EntraStatus` · `EntraLogin` · `EntraLogout`

**On-Call-Doku**
`OnCallDocList` · `OnCallDocGet` · `OnCallDocSave` · `OnCallDocSubmit` · `OnCallDocDismiss`

**Plugin-Admin** 🔒 ⏳ *(Install/Update laden herunter)*
`PluginList` · `PluginGetConfig` · `PluginSetConfig` · `PluginSetSecret` 🔒 · `PluginDeleteSecret` 🔒 · `PluginSetEnabled` · `PluginReload` · `PluginRefreshTags` · `ListPluginOrders` · `PluginListAvailable` · `PluginInstall` ⏳ · `PluginUpdate` ⏳ · `PluginUninstall`

**Feedback (GitHub Device Flow)** 🔒 ⏳
`FeedbackStatus` · `FeedbackStartDeviceLogin` · `FeedbackPollDeviceLogin` · `FeedbackLogout` · `FeedbackPreview` · `FeedbackSubmit`

**Hilfe / User-Docs**
`ListUserDocs` · `GetUserDoc`

**Sonstiges**
`Version` · `LogFrontend`

### Sonderbehandlung — nicht als RPC (9 Methoden)

| Methode | Heute | Nach dem Split |
|---|---|---|
| `Startup` | Wails `OnStartup` | Collector-Bootstrap (nicht UI) |
| `Shutdown` | Wails `OnShutdown` | Collector-Shutdown + Flush |
| `OnWindowBeforeClose` | Wails `OnBeforeClose` | UI-lokal (Fenster verstecken) |
| `Quit` | Tray/Signal → `wailsruntime.Quit` | Semantik klären (§7.2): Collector + UI beenden |
| `ShowWindow` | Tray „Öffnen" | UI-Prozess starten/fokussieren |
| `OpenHelpTab` | Tray „Hilfe" → Event + Show | Collector→UI-Event + UI-Start |
| `RequestSyncToday` | Tray „Sync" | Collector-intern (Preflight + Sync) |
| `FireQuickTag` | Hotkey-Callback | Collector-Hotkey → Push-Event |
| `QuickTagOpen` | von `FireQuickTag` | UI-lokal (Fenster positionieren/zeigen) |

## Anhang B – Events (Collector → UI Push-Stream)

Heute via `wailsruntime.EventsEmit`; nach dem Split über den API-Event-Stream, die UI re-emittet an die JS-Schicht (`EventsOn` bleibt unverändert):

| Event-Konstante | Zweck |
|---|---|
| `quick-tag-picker:open` / `:close` | Picker öffnen/schließen |
| `help:open` | Hilfe-Tab anzeigen |
| `startup-sync:result` | Ergebnis des Startup-Syncs |
| `startup-sync:conflict` | Preflight-Konflikt (Override/Import-Modal) |
| `PluginDiscoveredEvent` | neues Plugin entdeckt |
| `PluginStateChangedEvent` | Plugin-Statuswechsel |
| `OnCallDocChangedEvent` | On-Call-Doc geändert |
| `OnCallSubmitResultEvent` | Ergebnis einer Doc-Submission |

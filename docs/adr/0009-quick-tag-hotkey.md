# ADR 0009 – Quick-Tag-Picker über globalen Hotkey

- **Status:** Akzeptiert (retrospektiv dokumentiert)
- **Datum:** 2026-09-28
- **Bezug:** Spec §2.2a; `internal/winapi/hotkey_windows.go`, `internal/config/hotkey.go`, `QuickTagPicker.tsx`

## Kontext

Das Wechseln des manuellen Tags über das Tray-Menü erfordert mehrere Klicks und ist aus einer Vollbild-Anwendung heraus umständlich. Ein Kontextwechsel soll mit einem Tastendruck möglich sein.

## Entscheidung

- Registrierung eines **globalen Win32-Hotkeys** (`RegisterHotKey`) auf einem dedizierten, OS-gelockten Message-Loop-Thread; Default `Ctrl+Alt+T`, konfigurierbar (`quick_tag.hotkey`, `quick_tag.enabled`), mind. ein Modifier Pflicht.
- Da Wails v2 nur **ein Fenster** kennt, **übernimmt der Picker das Hauptfenster** temporär (340×420 px, untere rechte Ecke des Cursor-Monitors, AlwaysOnTop) und stellt Größe/Position/Sichtbarkeit danach wieder her.
- Inhalt: bis zu 10 Einträge (`0`–`9`), zuerst die in den letzten 30 Tagen genutzten Tags, dann Eltern-zuerst aufgefüllt; aktiver Tag markiert.
- Auswahl ruft `Orchestrator.StartManualOpenEnded` (ADR 0008); Auswahl des aktiven Tags ist No-op; `Esc`/erneuter Hotkey schließt.
- Registrierungsfehler (Hotkey belegt) ⇒ `Warn`-Log, Picker stumm; Nicht-Windows ⇒ `ErrUnsupported`.
- Nach dem Prozess-Split lebt der Hotkey im Collector; Fenster-Intents gehen als Control-Events an den UI-Prozess (ADR 0001).

## Konsequenzen

- ✅ Tag-Wechsel in < 1 s aus jeder Anwendung.
- ⚠️ Fenster-Übernahme statt eigenem Picker-Fenster; bei geschlossener UI Kaltstart-Latenz.
- ⚠️ Kollisionen mit anderen globalen Hotkeys (z. B. `Win+T`) möglich.

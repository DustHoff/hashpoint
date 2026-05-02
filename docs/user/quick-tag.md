# Quick-Tag-Picker

Der Quick-Tag-Picker ist die schnellste Möglichkeit, einen manuellen Tag zu wechseln, ohne das Hauptfenster zu öffnen oder das Tray-Menü zu bemühen. Mit einer einzigen Tastenkombination öffnet sich ein kompaktes Auswahlfenster unten rechts auf dem Monitor, auf dem sich gerade der Mauszeiger befindet — dort wählen Sie per Zifferntaste oder Mausklick aus den 10 zuletzt verwendeten Tags.

## Tastenkombination

- **Standard:** `Strg + Alt + T`
- Konfigurierbar im Tab **Einstellungen → Quick-Tag-Picker → Hotkey**.
- **Format:** `<Modifier>+<Modifier>+<Taste>` — Beispiele: `Ctrl+Alt+T`, `Win+Y`, `Shift+Ctrl+5`, `Ctrl+F12`.
- **Erlaubte Modifier:** `Ctrl`, `Alt`, `Shift`, `Win`. Mindestens ein Modifier ist Pflicht.
- **Erlaubte Tasten:** Buchstaben `A`–`Z`, Ziffern `0`–`9`, Funktionstasten `F1`–`F24`.

> ⚠️ **Nicht empfohlen:** `Win+T`. Diese Kombination ist von Windows belegt (Fokus auf die Taskleiste durchschalten) und sollte nicht überschrieben werden — andere Apps und Workflows verlassen sich darauf.

Schlägt die Registrierung fehl (z. B. weil eine andere Anwendung denselben Hotkey für sich beansprucht hat), wird das im Log auf `Warn`-Level vermerkt und der Picker reagiert nicht. Wählen Sie in den Einstellungen eine andere Kombination.

## Bedienung

1. **Hotkey drücken** — irgendwo, von jedem beliebigen Fenster aus.
2. Unten rechts auf dem Monitor des Cursors erscheint das Picker-Fenster.
3. **Tag auswählen:**
   - **Zifferntaste `0`–`9`** wählt den entsprechenden Eintrag direkt.
   - **Mausklick** auf einen Eintrag tut dasselbe.
   - **`Esc`** schließt den Picker ohne Wechsel.
   - **Erneut den Hotkey drücken** wirkt wie `Esc` (Toggle).

Sobald eine Auswahl getroffen ist, schließt das Picker-Fenster sich automatisch.

## Welche Tags werden angezeigt?

Der Picker listet bis zu **10 Tags**, nummeriert von `0` bis `9`:

1. **Zuletzt verwendete Tags der letzten 30 Tage**, sortiert vom zuletzt verwendeten zum am längsten zurückliegenden.
2. Stehen weniger als 10 zur Verfügung, werden die freien Slots mit **bisher unbenutzten Tags** aufgefüllt — in derselben Eltern-zuerst-Reihenfolge wie im Tray-Submenü und im Timeline-Picker.

Sub-Tags erscheinen mit dem Eltern-Namen als Präfix (`#projekta › #frontend`), damit gleichnamige Sub-Tags unterschiedlicher Eltern unterscheidbar bleiben. Der **aktuell aktive manuelle Tag** ist farblich hervorgehoben und mit „aktiv" gekennzeichnet — auch dann, wenn er gerade durch eine Auto-Tag-Phase pausiert ist.

## Was passiert bei der Auswahl?

- **Anderer Tag als aktuell aktiv:** Hashpoint schließt den laufenden manuellen Tag-Block (auf das Granularitätsraster gesnappt) und öffnet sofort einen neuen offenen Block für den gewählten Tag — exakt so, als hätten Sie den Tag im Tray-Submenü „Manueller Tag" angeklickt.
- **Bereits aktiver Tag:** Kein Effekt, der laufende Block läuft unverändert weiter (kein Re-Start). Der Picker schließt sich trotzdem.
- **Auto-Tag aktiv:** Wie im Tray-Submenü auch: der gewählte manuelle Tag wird gemerkt und springt an, sobald die Auto-Tag-Phase endet (siehe [Systemtray → Auto-Tag-Unterbrechung](tray.md#auto-tag-unterbrechung--automatische-fortsetzung)).

## Verhalten gegenüber dem Hauptfenster

Der Picker übernimmt für die Dauer der Auswahl das Hashpoint-Hauptfenster — Wails bietet (noch) kein Multi-Window. Konkret:

- Beim Hotkey-Druck merkt sich Hashpoint Größe und Position des Hauptfensters und schaltet es kurz auf `340×420` Pixel an die untere rechte Ecke des Cursor-Monitors um. War das Hauptfenster vorher sichtbar, bleibt es nach dem Schließen des Pickers auch sichtbar — Größe und Position werden zurückgesetzt. War es im Tray versteckt, verschwindet es nach Auswahl wieder.
- Der Picker liegt während der Anzeige immer im Vordergrund (`AlwaysOnTop`).

## Deaktivieren

Im Tab **Einstellungen → Quick-Tag-Picker** lässt sich das Feature komplett abschalten (Häkchen entfernen). Der Hotkey wird dann nicht mehr registriert; das Eingabefeld für die Tastenkombination ist gegraut.

## Plattform

Der Quick-Tag-Picker nutzt die Win32-API `RegisterHotKey` und steht ausschließlich unter Windows zur Verfügung.

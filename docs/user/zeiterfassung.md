# Zeitachse & Zeiterfassung

Der Tab **Zeitachse** ist die zentrale Arbeitsfläche im Alltag. Hier sehen Sie alle erfassten Blöcke eines Tages, taggen sie und stoßen die Personio-Synchronisation an.

## Wie wird erfasst?

Die Erfassung läuft automatisch im Hintergrund:

- Alle paar Sekunden (Standard: 2 s) prüft der TimeTracker, welches Fenster im Vordergrund ist.
- Wechselt das aktive Fenster (anderer Prozess oder anderer Fenstertitel), wird der bisherige **Fokus-Block** geschlossen und ein neuer geöffnet.
- Erfasst werden: Prozessname (z. B. `chrome.exe`), Prozesspfad, Fenstertitel, Start- und Endzeit (UTC).
- Erkennt das System eine längere Idle-Phase (Standard: 5 Minuten ohne Tastatur-/Maus-Eingabe), wird der laufende Block beendet und als **Idle** markiert.
- Bildschirmsperre oder Logout schließen den aktuellen Block sofort.
- Stürzt die Anwendung ab, wird beim nächsten Start ein offen gebliebener Block automatisch sauber abgeschlossen.

## Aufbau des Tabs

Der Tab ist dreigeteilt:

1. **Kopfbereich** – Datum, Pause/Sync.
2. **Zeitstrahl-Streifen** – horizontaler 24-Stunden-Strip mit allen Blöcken.
3. **Tabellenliste** – jeder Block einzeln mit Prozess, Titel, Tag und
   Beschreibung.

### Kopfbereich

| Element | Funktion |
| --- | --- |
| **Datums-Eingabe** | Datum wählen (klicken oder `YYYY-MM-DD` eingeben). |
| **← Vortag** | Springt einen Tag zurück. |
| **Folgetag →** | Springt einen Tag vor. |
| **Summe: X h Y m** | Summe der erfassten Zeit (ohne Idle-Blöcke). |
| **Pausieren / Fortsetzen** | Hält die Erfassung an oder setzt sie fort. |
| **Sync zu Personio** | Überträgt den aktuellen Tag an Personio. Während der Übertragung steht *Synchronisiere…*. |

### Zeitstrahl-Streifen

Direkt unter dem Kopfbereich liegt ein horizontaler **Zeitstrahl** vom Tagesanfang
(00:00) bis Tagesende (24:00):

- **Tag-Segmente:** Direkt aufeinanderfolgende Blöcke mit dem **gleichen Tag**
  werden zu einem zusammenhängenden farbigen Segment in der Tag-Farbe verschmolzen.
- **Idle-Blöcke** erscheinen als blasser, ausgegrauter Streifen.
- **Ungetaggte Blöcke** werden grau dargestellt.
- **Stundenraster:** feine Trennlinien alle 60 Minuten, Beschriftung 00 / 06 / 12 / 18 / 24.

#### Selektion via Strip

| Geste | Wirkung |
| --- | --- |
| **Klick auf ein Tag-Segment** | Selektiert alle Blöcke des Segments. |
| **Shift+Klick auf ein Segment** | Erweitert die bestehende Auswahl additiv. |
| **Drag (linke Maustaste)** | Zieht eine Zeitspanne auf. Beim Loslassen werden alle Blöcke, die diesen Bereich schneiden, ausgewählt. |
| **Shift+Drag** | Wie Drag, aber additiv zur bestehenden Auswahl. |

Der aktive Auswahlbereich wird während des Ziehens als blaues Overlay
hervorgehoben.

#### Hover-Highlight

Beim **Mouse-Over auf ein Tag-Segment** werden in der darunterliegenden
Tabelle die zugehörigen Programmzeilen farblich hervorgehoben. So sehen Sie auf
einen Blick, welche konkreten Programme zu einem Tag-Block gehören – auch wenn
mehrere Anwendungen (z. B. IDE, Browser, Terminal) ineinandergreifen.

### Block-Liste

Jeder Block in der Liste zeigt:

- **Zeitraum:** `HH:MM–HH:MM` (oder nur Startzeit bei laufendem Block)
- **Dauer:** `X m Y s`
- **Prozessname:** z. B. `code.exe`
- **Fenstertitel:** abgeschnitten, vollständig im Tooltip
- **Beschreibung:** sofern vergeben, mit Stift-Symbol 📝 abgekürzt eingeblendet
- **Tag:** Farb-Chip mit Tag-Name; **⚙** kennzeichnet automatisch zugewiesene Tags
- **Idle-Blöcke:** abgeblendet (50 % Deckkraft)
- **Hover aus dem Strip:** wird die zugehörige Zeile leicht hervorgehoben

Die Zeitachse aktualisiert sich automatisch alle 5 Sekunden, solange der Tab geöffnet ist.

## Blöcke taggen

1. Auswahl treffen – wahlweise:
   - In der Tabelle einen Block anklicken (Shift-Klick = Bereich)
   - Im Strip ein Tag-Segment klicken (Shift-Klick = additiv)
   - Im Strip mit der Maus eine Zeitspanne ziehen (Shift-Drag = additiv)
2. Sobald mindestens ein Block markiert ist, erscheint das Panel
   *„N Block(s) markiert →"* mit den verfügbaren Tags und einem Textfeld für
   die **Tätigkeitsbeschreibung**.
3. Optional eine Beschreibung tippen.
4. Auf einen Tag-Button klicken → Tag **und** Beschreibung werden in einem
   Schritt allen markierten Blöcken zugewiesen.
5. Soll nur die Beschreibung geändert werden, ohne den Tag anzufassen,
   die Beschreibung tippen und **Beschreibung speichern** klicken.
6. **Tag entfernen** → entfernt das Tagging der ausgewählten Blöcke (die
   Beschreibung bleibt erhalten).
7. **Auswahl aufheben** → leert die Markierung.

> Manuell vergebene Tags überschreiben Auto-Tags und werden bei späterem Lauf der Auto-Tagging-Engine **nicht** überschrieben.

### Tätigkeitsbeschreibung

Jeder Block kann zusätzlich zum Tag eine freie Tätigkeitsbeschreibung tragen
(z. B. „Refactoring Login-Flow", „Code-Review PR #123"). Die typische Anwendung
ist ein zusammenhängender Tag-Block, der mehrere Programme umfasst — IDE,
Browser, Terminal — und bei dem Sie der Gesamttätigkeit einen einzelnen
Beschreibungstext geben möchten:

1. Auf dem Strip das Tag-Segment anklicken oder die Zeitspanne aufziehen.
2. Im Auswahlpanel die Beschreibung tippen.
3. **Tag-Button** drücken (überschreibt Tag + Beschreibung) oder
   **Beschreibung speichern** (lässt vorhandenes Tagging unverändert).

Bei der Personio-Synchronisation wird die Beschreibung an den aus den
Tag-Namen erzeugten Kommentar angehängt:
`#projekta #frontend Refactoring Login-Flow — Login-Bug aus Ticket #123`.

## Pause & Fortsetzen

- **Pausieren:** Schließt den aktuell laufenden Block sofort und beendet die Hintergrund-Erfassung. Während der Pause werden keine neuen Blöcke geöffnet.
- **Fortsetzen:** Aktiviert die Erfassung wieder. Der nächste Fensterwechsel öffnet einen neuen Block.

Pausieren ist nützlich, wenn Sie an etwas Privatem oder nicht erfasswürdigem arbeiten. Der Status lässt sich auch über das Tray-Menü umschalten.

## Bestehende Blöcke korrigieren

Über die Backend-Schnittstelle stehen folgende Operationen bereit (in der UI werden Sie unter „Bearbeiten"-Aktionen am Block sichtbar):

- **Block teilen** – Splittet einen Block an einer beliebigen Uhrzeit in zwei separate Blöcke. Praktisch, wenn ein Auto-Tag-Wechsel innerhalb eines langen Blocks gewünscht ist.
- **Block bearbeiten** – Ändern von Startzeit, Endzeit und Fenstertitel.
- **Block löschen** – Entfernt den Block komplett. Eine Wiederherstellung ist nicht möglich.

> Nicht editierbar sind Prozessname, Dauer (wird automatisch berechnet) und der Idle-Status. Diese Felder werden ausschließlich von der Erfassung gesetzt.

## Fehlerbanner

Schlägt eine Aktion fehl (z. B. Sync mit Personio), erscheint im Tab ein rotes Banner mit der Fehlermeldung. Häufige Ursachen:

- Personio-Zugangsdaten unvollständig oder ungültig
- Keine Internet-Verbindung
- Tag ohne gültige Personio-Projekt-/Aktivitäts-ID

Details siehe [Personio-Synchronisation](personio.md).

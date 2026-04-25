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

### Kopfbereich

| Element | Funktion |
| --- | --- |
| **Datums-Eingabe** | Datum wählen (klicken oder `YYYY-MM-DD` eingeben). |
| **← Vortag** | Springt einen Tag zurück. |
| **Folgetag →** | Springt einen Tag vor. |
| **Summe: X h Y m** | Summe der erfassten Zeit (ohne Idle-Blöcke). |
| **Pausieren / Fortsetzen** | Hält die Erfassung an oder setzt sie fort. |
| **Sync zu Personio** | Überträgt den aktuellen Tag an Personio. Während der Übertragung steht *Synchronisiere…*. |

### Block-Liste

Jeder Block in der Liste zeigt:

- **Zeitraum:** `HH:MM–HH:MM` (oder nur Startzeit bei laufendem Block)
- **Dauer:** `X m Y s`
- **Prozessname:** z. B. `code.exe`
- **Fenstertitel:** abgeschnitten, vollständig im Tooltip
- **Tag:** Farb-Chip mit Tag-Name; **⚙** kennzeichnet automatisch zugewiesene Tags
- **Idle-Blöcke:** abgeblendet (50 % Deckkraft)

Die Zeitachse aktualisiert sich automatisch alle 5 Sekunden, solange der Tab geöffnet ist.

## Blöcke taggen

1. Im Block-Liste auf einen Block klicken → der Block wird markiert (dunkler Hintergrund).
2. Mehrere Blöcke markieren:
   - **Klick** auf weitere Blöcke (toggelt die Auswahl)
   - **Shift-Klick** wählt einen zusammenhängenden Bereich
3. Ist mindestens ein Block markiert, erscheint oben ein Panel mit dem Hinweis *„N Block(s) markiert →"* und den verfügbaren Tags.
4. Auf einen Tag-Button klicken → der Tag wird allen markierten Blöcken zugewiesen.
5. **Tag entfernen** → entfernt das Tagging der ausgewählten Blöcke.

> Manuell vergebene Tags überschreiben Auto-Tags und werden bei späterem Lauf der Auto-Tagging-Engine **nicht** überschrieben.

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

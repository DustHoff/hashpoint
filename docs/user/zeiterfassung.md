# Zeitachse & Zeiterfassung

Der Tab **Zeitachse** ist die zentrale Arbeitsfläche im Alltag. Hier sehen Sie drei übereinander liegende Zeitstrahlen — oben die **Tag-Blöcke** (manuell und automatisch vergeben), in der Mitte die **Fokus-Prozesse** (was Sie tatsächlich auf dem Bildschirm hatten), unten die **📞 Kommunikations-Prozesse** (Teams, Zoom, … parallel zum Fokus) — und können beides taggen, korrigieren oder an Personio übertragen.

## Wie wird erfasst?

Die Erfassung ist in drei Schichten unterteilt, die unabhängig voneinander gepflegt werden:

- **Fokus-Process-Tracks** (rohe Fokus-Daten): Alle paar Sekunden (Standard: 2 s) prüft der TimeTracker, welches Fenster im Vordergrund ist. Wechselt das Fenster, wird der bisherige Track geschlossen und ein neuer geöffnet. Diese Schicht kennt **keine** Granularität — die Zeiten sind sekundengenau.
- **Kommunikations-Process-Tracks** (parallel zum Fokus): Sobald ein in den [Einstellungen](einstellungen.md#kommunikations-prozesse) eingetragenes Kommunikationsprogramm (Default: `teams.exe`) ein **sichtbares** Fenster zeigt, wird unabhängig von der Fokus-Erfassung ein zweiter Track angelegt. Das ist genau dann erwünscht, wenn Sie z. B. in einem Teams-Meeting sind und nebenher in Browser oder Editor arbeiten — die Meeting-Zeit fällt dann nicht unter den Tisch.
- **Tag-Blöcke** (Tagging-Spannen): Auf einer separaten Ebene werden Tag-Blöcke gepflegt, entweder automatisch durch Auto-Tagging-Regeln oder manuell. Tag-Blöcke snappen auf das eingestellte Granularitätsraster (`:00/:15/:30/:45` bei `15`). Sie sind die Quelle für den Personio-Sync.

Idle-Erkennung, Bildschirmsperre und Crash-Recovery wirken auf der Fokus-Process-Track-Ebene; offene manuelle Tag-Blöcke werden beim nächsten Start automatisch geschlossen. Kommunikations-Tracks ignorieren Idle bewusst (man kann einem Meeting auch ohne Tastatur folgen) — sie schließen erst, wenn das zugehörige Fenster geschlossen wird.

## Aufbau des Tabs

Das Hashpoint-Fenster startet **maximiert**, damit beide Listen direkt nebeneinander Platz haben. Es bleibt ein normales Fenster — verschiebbar, verkleinerbar, schließbar in den Tray.

Der Tab besteht aus:

1. **Kopfbereich** – Datum, Pause/Sync.
2. **Drei Zeitstrahl-Streifen** – oben die Tag-Blöcke, in der Mitte die Fokus-Process-Tracks, unten die Kommunikations-Tracks. Alle drei teilen sich Zoom und Scroll.
3. **Zwei nebeneinander liegende Listen** unterhalb der Streifen:
   - **Links — Tag-Block-Liste:** ein Eintrag pro Tag-Block (manuell oder auto), mit Beschreibung und Tag-Chips. Bekommt 40 % der Breite.
   - **Rechts — Process-Track-Liste:** gruppiert sowohl Fokus- als auch Kommunikations-Tracks nach Programm; Kommunikations-Zeilen sind mit einem 📞-Symbol vor dem Programmnamen markiert. Bekommt 60 % der Breite, weil Fenstertitel länger sind als Tag-Chips.

   Beide Listen scrollen unabhängig voneinander (jeweils max. 65 % der Bildschirmhöhe), damit ein langer Tag links nicht die Sicht auf die Prozesse rechts verschiebt.

### Kopfbereich

| Element | Funktion |
| --- | --- |
| **Datums-Eingabe** | Datum wählen (klicken oder `YYYY-MM-DD` eingeben). |
| **← Vortag** | Springt einen Tag zurück. |
| **Folgetag →** | Springt einen Tag vor. |
| **Getaggt: X h Y m** | Summe der getaggten Zeit (Summe aller Tag-Blöcke). |
| **Pausieren / Fortsetzen** | Hält die Erfassung an oder setzt sie fort. |
| **Sync zu Personio** | Überträgt die Tag-Blöcke des aktuellen Tages an Personio. Während der Übertragung steht *Synchronisiere…*. |

### Zeitstrahl-Streifen

Direkt unter dem Kopfbereich liegen zwei horizontale Streifen vom Tagesanfang (00:00) bis Tagesende (24:00):

#### Top-Strip — Tags

- **Tag-Blöcke** werden in der Tag-Farbe als farbige Balken dargestellt.
- **Auto-Tag-Blöcke** haben eine **gestrichelte Umrandung**, manuelle Tag-Blöcke sind ohne Umrandung.
- **Stundenraster:** feine Trennlinien alle 60 Minuten.
- **Drag (linke Maustaste):** Zieht eine Zeitspanne auf. Die Auswahl snappt live auf das Granularitätsraster.
- **Klick auf einen Tag-Block:** Selektiert den Block (Shift = additiv).
- **Hover:** Filtert die Process-Tabelle auf den Zeitraum des Blocks.

#### Mittel-Strip — Fokus-Prozesse

- **Read-only**: zeigt die rohen Fokus-Events des Trackers.
- **Idle-Bereiche** erscheinen blass und ausgegraut.
- Jeder Prozessname bekommt eine deterministische Farbe, damit aufeinanderfolgende verschiedene Programme visuell trennbar sind.
- **Hover** filtert die Process-Tabelle auf den Zeitraum des Tracks.

#### Bottom-Strip — 📞 Kommunikation

- Eine **eigene Schiene** für die in den [Einstellungen](einstellungen.md#kommunikations-prozesse) gelisteten Kommunikationsprogramme — Default `teams.exe`. Die Schiene teilt sich Zoom und Scrollposition mit den beiden anderen Streifen.
- Jeder Eintrag entspricht einem **sichtbaren Top-Level-Fenster** des Programms (Meeting-Fenster, Hauptfenster, Anruf-Popup …). Ist das Programm nur im Tray, gibt es **keinen** Eintrag — Hashpoint trackt nur, was Sie tatsächlich auf dem Bildschirm haben.
- Segmente sind mit einer dezenten grünen Umrandung und einem 📞-Glyph (bei genug Platz) versehen, damit sie auf einen Blick unterscheidbar bleiben.
- **Hover** filtert die Process-Tabelle auf den Zeitraum des Kommunikations-Tracks.
- An Tagen ohne Kommunikations-Aktivität zeigt die Schiene den Hinweis *„Keine Kommunikations-Aktivität an diesem Tag"*.

#### Zoom & Pan

| Geste | Wirkung |
| --- | --- |
| **Mausrad** | Zoomt cursor-anchored in alle drei Strips gleichzeitig. |
| **Shift + Mausrad** | Schwenkt horizontal. |
| **Doppelklick** | Setzt Zoom auf den ganzen Tag zurück. |

### Tag-Block-Liste (linke Spalte)

Pro Eintrag:

- **Zeitraum** `HH:MM–HH:MM`
- **Dauer** `X m Y s`
- **manuell|auto** — markiert die Herkunft
- **Beschreibung** (sofern vergeben, mit 📝 abgekürzt)
- **Tag-Chip** (bei Sub-Tags zusätzlich der Eltern-Tag-Chip)

Klick toggelt die Selektion (Shift = additiv).

### Process-Track-Liste (rechte Spalte)

Aufeinanderfolgende Tracks mit gleichem Programm werden zu einer Zeile zusammengefasst. Der Pfeil-Button expandiert die Gruppe und zeigt jeden ursprünglichen Fenstertitel — nützlich, um lange Browser-Sitzungen mit vielen Tabs nachzuvollziehen.

**Kommunikations-Tracks** (z. B. Teams) erscheinen in derselben Liste, sind aber durch ein 📞-Symbol vor dem Programmnamen gekennzeichnet. Da sie sich zeitlich mit Fokus-Tracks überlappen können, sehen Sie für denselben Zeitraum ggf. zwei Einträge — einen für den fokussierten Vordergrund-Prozess und einen für das parallele Kommunikations-Fenster. Die getrennte Gruppierung verhindert, dass Fokus- und Kommunikations-Tracks desselben Prozessnamens zu einer Zeile vermischt werden.

Diese Liste ist read-only — Process-Tracks repräsentieren die Realität und können nicht editiert werden. Korrekturen passieren auf der Tag-Block-Ebene.

## Tag-Blöcke anlegen oder ändern

### Manuelle Range über Drag

1. Im Top-Strip mit der Maus eine Zeitspanne aufziehen. Die Auswahl snappt auf das Granularitätsraster (sichtbar als blaues Overlay).
2. Optional die Kanten der Range mit den Greifern fein nachziehen.
3. Im Auswahl-Panel optional eine Beschreibung eingeben.
4. Auf einen Tag-Button klicken → ein **manueller** Tag-Block entsteht.
   - Überlappende Auto-Tag-Blöcke werden automatisch getrimmt, gesplittet oder gelöscht (Manual gewinnt).
   - Überlappung mit einem bestehenden manuellen Tag-Block wird abgelehnt — den Konflikt-Block zuerst löschen oder ändern.

### Bestehende Tag-Blöcke nachbearbeiten

1. Im Top-Strip oder in der Tag-Block-Tabelle den/die Block(s) anklicken.
2. Im Auswahl-Panel:
   - **Tag-Button** ändert den Tag (und schreibt die Beschreibung).
   - **Speichern** ändert nur die Beschreibung.
   - **Löschen** entfernt die ausgewählten Tag-Blöcke endgültig (Process-Tracks bleiben unberührt). Alternativ: Taste **Entf** / **Delete** — gleiche Sicherheitsabfrage, gleiches Verhalten. Die Tastatur-Abkürzung wird ignoriert, solange der Cursor in einem Eingabefeld (z. B. Beschreibung) steht, damit dort weiter normal Text gelöscht werden kann.
   - **Auswahl aufheben** leert die Markierung.

### Tag-Block-Kanten ziehen (Resize)

Sobald **genau ein abgeschlossener** Tag-Block selektiert ist, erscheinen am linken und rechten Blockrand zwei weiße Greifer. Damit lässt sich der Block in der Zeitachse strecken oder stauchen, ohne ihn neu anzulegen:

- Greifer mit der Maus nach links/rechts ziehen — die Kante snappt live auf das Granularitätsraster.
- **Nachbarschutz:** Die Kante stoppt automatisch an der Grenze des nächsten Tag-Blocks. In bereits getaggte Bereiche kann nicht gezogen werden — nur in „freien" Zeitraum (oder bis zur Tageskante 00:00 bzw. 24:00).
- Der Block muss mindestens eine Granularitätsstufe breit bleiben — die gezogene Kante kann nicht über die gegenüberliegende Kante hinausgehen.
- **Auto-Tag-Blöcke** werden beim Resize automatisch zu manuellen Blöcken (gestrichelte Umrandung verschwindet), damit die Auto-Tag-Engine sie nicht beim nächsten Lauf wieder regeneriert.
- **Offene Blöcke** (der laufende Auto-Tag, der offene manuelle Tag aus dem Tray) sind nicht resizable — sie haben kein festes Ende, das man verschieben könnte.

Die Greifer verschwinden, sobald mehrere Blöcke selektiert sind, ein neuer manueller Range gezogen wird, oder die Auswahl aufgehoben wird.

### Tag-Buttons im Auswahl-Panel

Die Tag-Buttons sind **nach Eltern-Tag gruppiert**: Erst der Eltern-Tag, direkt dahinter dessen Sub-Tags. Sub-Tag-Buttons zeigen zusätzlich den **Eltern-Namen als gedimmtes Präfix**, getrennt durch ein `›`:

```
#projekta   #projekta › #frontend   #projekta › #meeting   #projektb   #projektb › #meeting
```

So bleiben gleichnamige Sub-Tags unterschiedlicher Projekte (z. B. zwei `#meeting` unter verschiedenen Eltern) eindeutig unterscheidbar. Der vollständige Pfad `Eltern › Sub` steht zusätzlich im Tooltip, falls der Button-Text abgeschnitten ist.

### Tätigkeitsbeschreibung

Jeder Tag-Block kann eine freie Tätigkeitsbeschreibung tragen (z. B. „Refactoring Login-Flow", „Code-Review PR #123"). Die Beschreibung wird beim Personio-Sync an den aus den Tag-Namen erzeugten Kommentar angehängt:

`#projekta #frontend Refactoring Login-Flow — Login-Bug aus Ticket #123`

Bei einem **offenen** manuellen Tag (siehe [Systemtray](tray.md)) wird die beim Start hinterlegte Beschreibung auch nach einer Auto-Tag-Unterbrechung erneut in den fortgesetzten manuellen Block übernommen.

## Pause & Fortsetzen

- **Pausieren:** Schließt den aktuellen Process-Track sofort und beendet die Hintergrund-Erfassung. Während der Pause öffnen sich keine neuen Process-Tracks und keine Auto-Tag-Blöcke. Ein offener manueller Tag-Block bleibt unangetastet — er gilt weiter, bis Sie ihn explizit stoppen oder die Erfassung wieder läuft.
- **Fortsetzen:** Aktiviert die Erfassung wieder. Der nächste Fensterwechsel öffnet einen neuen Process-Track; passende Auto-Tag-Regeln greifen wieder.

Pausieren ist nützlich, wenn Sie an etwas Privatem oder nicht erfasswürdigem arbeiten. Der Status lässt sich auch über das Tray-Menü („Pause Tracking") oder die Einstellungen umschalten und wird persistiert — nach einem Neustart bleibt die letzte Wahl erhalten.

## Fehlerbanner

Schlägt eine Aktion fehl (z. B. Sync mit Personio), erscheint im Tab ein rotes Banner mit der Fehlermeldung. Häufige Ursachen:

- Personio-Zugangsdaten unvollständig oder ungültig
- Keine Internet-Verbindung
- Tag ohne gültige Personio-Projekt-/Aktivitäts-ID
- Versuch, einen manuellen Range-Tag auf einem bestehenden manuellen Block anzulegen (Konflikt)

Details siehe [Personio-Synchronisation](personio.md).

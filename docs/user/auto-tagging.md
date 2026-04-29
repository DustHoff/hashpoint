# Auto-Tagging-Regeln

Auto-Tagging-Regeln erzeugen automatisch **Tag-Blöcke**, sobald ein passender Prozess fokussiert wird – auf Basis von Prozessname und/oder Fenstertitel. So müssen Sie häufig wiederkehrende Tätigkeiten nicht jedes Mal manuell taggen.

## Was passiert beim Auto-Tagging?

Anders als bei einer reinen „pro Block"-Markierung pflegt der TimeTracker einen **Auto-Tag-Block** mit eigenem Lebenszyklus:

1. Sie wechseln in einen Browser; die Regel „Prozess enthält `chrome` → `#web`" trifft.
2. Der TimeTracker öffnet einen Auto-Tag-Block `#web` (Start auf das Granularitätsraster gesnappt — z. B. `09:00` bei einer 15-Min-Granularität, auch wenn Sie eigentlich um `09:07` eingestiegen sind).
3. Solange Sie in einem Programm bleiben, das die **gleiche** Regel trifft (egal welches `chrome`-Fenster), bleibt der Auto-Block offen.
4. Sobald Sie auf ein nicht-passendes Programm wechseln (oder idle gehen), schließt der Block (End auf das Raster floored).
5. Eine kürzere Match-Phase, die das Granularitätsraster nicht erreicht, erzeugt **keinen** Tag-Block — Auto-Blöcke unterhalb der Granularität werden unterdrückt.

Auto-Tag-Blöcke sind im Tag-Strip an der **gestrichelten Umrandung** erkennbar, manuelle Tag-Blöcke haben keine Umrandung.

## Aufbau des Tabs

Der Tab **Auto-Tagging** ist zweispaltig:

- **Linke Spalte – Regel-Liste:** Alle Regeln, sortiert nach Priorität (höchste zuerst).
- **Rechte Spalte – Editor & Live-Test:** Formular zum Anlegen/Bearbeiten und Test der Regel gegen einen Tag.

## Eine Regel anlegen

1. Tab **Auto-Tagging** öffnen.
2. Im Editor das Formular ausfüllen.
3. Optional: Live-Test ausführen.
4. **Speichern** klicken.

### Felder im Editor

| Feld | Werte | Bedeutung |
| --- | --- | --- |
| **Feld** | `Prozess`, `Fenstertitel`, `Beide` | Welcher Teil eines Process-Tracks für die Übereinstimmung herangezogen wird. Bei `Beide` müssen Prozess **und** Titel matchen. |
| **Typ** | `enthält`, `gleich`, `Regex (RE2)` | Wie der Pattern-Vergleich erfolgt. `enthält` und `gleich` sind case-insensitiv. |
| **Pattern** | Text oder Regex | Der zu vergleichende Wert. Bei `Regex` Go-RE2-Syntax (kein Backtracking). |
| **Ziel-Tag** | Tag aus Liste | Welcher Tag dem Auto-Tag-Block zugewiesen wird. |
| **Priorität** | Integer (Default `0`) | Höhere Werte werden zuerst geprüft. Erste passende Regel gewinnt. |
| **aktiviert** | Checkbox (Default an) | Deaktivierte Regeln werden ignoriert. |

### Match-Typen im Detail

- **`enthält`** – sucht den Pattern als Teilstring (case-insensitiv). Beispiel: Pattern `chrome` matcht `Google Chrome`, `chrome.exe`, `CHROMECAST`.
- **`gleich`** – Pattern muss exakt gleich sein (case-insensitiv). Beispiel: Pattern `chrome.exe` matcht `chrome.exe`, aber nicht `googlechrome.exe`.
- **`Regex (RE2)`** – Go-RE2-Regex. Keine Backtracking-Konstrukte (kein `\1`, kein Lookahead). Beispiel: `^slack(\.exe)?$` matcht `slack` und `slack.exe`.

### Priorität & Reihenfolge

- Regeln werden nach **Priorität absteigend** ausgewertet.
- Bei **gleicher Priorität** ist die Reihenfolge nicht garantiert – arbeiten Sie mit Lücken (`0`, `10`, `20`), um eindeutige Reihenfolgen zu erzwingen.
- Sobald eine Regel matcht, wird der Auto-Block geöffnet; weitere Regeln werden **nicht** mehr geprüft.

> **Faustregel:** Spezifischere Regeln bekommen höhere Priorität, allgemeine Fallback-Regeln eine niedrigere.

### Zusammenspiel mit dem Tracking-Schalter und dem manuellen Tagging

- **Tracking deaktiviert** (Tray „Pause Tracking" oder Einstellungen *„Erfassung der fokussierten Anwendung aktiv"*): Der TimeTracker erfasst keine neuen Process-Tracks, also greifen die Auto-Tagging-Regeln nicht. Der Tab **Auto-Tagging** bleibt aber jederzeit erreichbar — Regeln lassen sich im pausierten Zustand anlegen, bearbeiten und testen.
- **Manuelles Tagging aktiv** (Tray-Submenü „Manueller Tag"): Eine offene manuelle Sitzung wird durch Auto-Tags **temporär unterbrochen** — der manuelle Block schließt am Auto-Tag-Start, der Auto-Block läuft, und sobald das Auto-Match endet, öffnet automatisch ein neuer manueller Block mit demselben Tag und derselben Beschreibung. Details siehe [Systemtray](tray.md#auto-tag-unterbrechung--automatische-fortsetzung).
- **Manuelle Range übersteuert Auto:** Eine im Top-Strip per Drag erzeugte manuelle Range ersetzt überlappende Auto-Tag-Blöcke (sie werden getrimmt, gesplittet oder gelöscht). Auto-Tags überschreiben **nie** manuelle Tag-Blöcke.

## Live-Test (vor dem Speichern)

Vor dem Speichern können Sie eine Regel gegen reale Daten testen:

1. Pattern und Ziel-Tag im Editor ausfüllen.
2. Im Bereich **Live-Test** ein Datum wählen (Default: heute).
3. **Testen** klicken.
4. Es erscheint eine Liste der Process-Tracks dieses Tages:
   - **✓** (grün) = die Regel würde diesen Track auslösen
   - **·** (grau) = die Regel matcht nicht
5. Pattern anpassen und erneut testen, bis die Treffer passen.
6. **Speichern** klicken.

> Der Live-Test schreibt **nichts** in die Datenbank – er zeigt nur, welche Process-Tracks matchen würden.

## Regel bearbeiten oder löschen

- **Bearbeiten:** Auf den Eintrag in der Regel-Liste klicken → Felder im Editor anpassen → **Speichern**.
- **Löschen:** **Löschen**-Button am Eintrag → Sicherheitsabfrage bestätigen.

## Beispiele

| Anwendungsfall | Feld | Typ | Pattern | Priorität |
| --- | --- | --- | --- | --- |
| Alles in IntelliJ → `#Coding` | Prozess | enthält | `idea` | 10 |
| Slack → `#Communication` | Prozess | gleich | `slack.exe` | 20 |
| Browser-Tickets in Jira → `#TicketWork` | Fenstertitel | Regex | `(?i)\bjira\b` | 30 |
| Alle Browser → `#Recherche` (Fallback) | Prozess | Regex | `^(chrome\|firefox\|edge)\.exe$` | 0 |

In diesem Beispiel würde ein Browser-Fenster mit Jira-Titel den Tag `#TicketWork` bekommen (höhere Priorität), andere Browser-Fenster fallen auf `#Recherche` zurück.

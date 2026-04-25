# Auto-Tagging-Regeln

Auto-Tagging-Regeln weisen neuen Fokus-Blöcken automatisch einen Tag zu, sobald sie geöffnet werden – auf Basis von Prozessname und/oder Fenstertitel. So müssen Sie häufig wiederkehrende Tätigkeiten nicht jedes Mal manuell taggen.

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
| **Feld** | `Prozess`, `Fenstertitel`, `Beide` | Welcher Teil eines Blocks für die Übereinstimmung herangezogen wird. Bei `Beide` müssen Prozess **und** Titel matchen. |
| **Typ** | `enthält`, `gleich`, `Regex (RE2)` | Wie der Pattern-Vergleich erfolgt. `enthält` und `gleich` sind case-insensitiv. |
| **Pattern** | Text oder Regex | Der zu vergleichende Wert. Bei `Regex` Go-RE2-Syntax (kein Backtracking). |
| **Ziel-Tag** | Tag aus Liste | Welcher Tag dem Block zugewiesen wird, wenn die Regel matcht. |
| **Priorität** | Integer (Default `0`) | Höhere Werte werden zuerst geprüft. Erste passende Regel gewinnt. |
| **aktiviert** | Checkbox (Default an) | Deaktivierte Regeln werden ignoriert. |

### Match-Typen im Detail

- **`enthält`** – sucht den Pattern als Teilstring (case-insensitiv). Beispiel: Pattern `chrome` matcht `Google Chrome`, `chrome.exe`, `CHROMECAST`.
- **`gleich`** – Pattern muss exakt gleich sein (case-insensitiv). Beispiel: Pattern `chrome.exe` matcht `chrome.exe`, aber nicht `googlechrome.exe`.
- **`Regex (RE2)`** – Go-RE2-Regex. Keine Backtracking-Konstrukte (kein `\1`, kein Lookahead). Beispiel: `^slack(\.exe)?$` matcht `slack` und `slack.exe`.

### Priorität & Reihenfolge

- Regeln werden nach **Priorität absteigend** ausgewertet.
- Bei **gleicher Priorität** ist die Reihenfolge nicht garantiert – arbeiten Sie mit Lücken (`0`, `10`, `20`), um eindeutige Reihenfolgen zu erzwingen.
- Sobald eine Regel matcht, wird der Block getaggt; weitere Regeln werden **nicht** mehr geprüft.

> **Faustregel:** Spezifischere Regeln bekommen höhere Priorität, allgemeine Fallback-Regeln eine niedrigere.

## Live-Test (vor dem Speichern)

Vor dem Speichern können Sie eine Regel gegen reale Daten testen:

1. Pattern und Ziel-Tag im Editor ausfüllen.
2. Im Bereich **Live-Test** ein Datum wählen (Default: heute).
3. **Testen** klicken.
4. Es erscheint eine Liste von Blöcken aus dem gewählten Tag:
   - **✓** (grün) = die Regel würde diesen Block taggen
   - **·** (grau) = die Regel matcht nicht
5. Pattern anpassen und erneut testen, bis die Treffer passen.
6. **Speichern** klicken.

> Der Live-Test schreibt **nichts** in die Datenbank – er zeigt nur, welche Blöcke matchen würden.

## Regel auf Historie anwenden

Frisch angelegte Regeln greifen erst für **neue** Blöcke. Bestehende Blöcke kann man nachträglich nachtaggen:

1. In der Regel-Liste auf **Auf Historie anwenden** klicken.
2. Bestätigen.
3. Der TimeTracker geht alle **ungetaggten** Blöcke der letzten zwei Jahre durch und tagged passende Blöcke.
4. Eine Meldung zeigt die Anzahl neu getaggter Blöcke.

> Bereits getaggte Blöcke (egal ob manuell oder durch eine andere Regel) werden **nicht** überschrieben. Wer alte Tags ersetzen will, muss sie zuerst manuell entfernen oder den Tag ändern.

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

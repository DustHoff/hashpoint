# Systemtray

Der TimeTracker läuft im Hintergrund und ist über das Tray-Symbol (Hashpoint-Icon) im Windows-Benachrichtigungsbereich (rechts unten neben der Uhr) jederzeit erreichbar.

## Tooltip

Beim Überfahren des Icons mit der Maus erscheint:

```
Hashpoint TimeTracker <Version>
```

So sehen Sie auf einen Blick, ob die Anwendung läuft und in welcher Version.

## Hauptfenster öffnen

- **Klick** auf das Tray-Icon zeigt das Hauptfenster (Zeitachse, Tags, Auto-Tagging, Über).
- Das Schließen-Kreuz im Fenster minimiert die Anwendung in den Tray. Die Erfassung läuft weiter.

## Kontextmenü

Per **Rechtsklick** auf das Tray-Icon öffnet sich das Menü:

| Eintrag | Funktion |
| --- | --- |
| **Öffnen** | Bringt das Hauptfenster nach vorne. |
| **Pause Tracking** *(Checkbox)* | Hält das automatische Fokus-Tracking an oder setzt es fort. Häkchen = pausiert. Beim Pausieren wird der laufende Process-Track sofort sauber abgeschlossen. Manuelles Tagging (siehe unten) bleibt unabhängig davon möglich. |
| **Sync zu Personio (heute)** | Synchronisiert die Tag-Blöcke des heutigen Tages direkt an Personio, ohne das Hauptfenster zu öffnen. |
| **Manueller Tag** *(Submenü)* | Startet oder beendet eine offene manuelle Tag-Sitzung. Siehe Abschnitt **Manuelles Tagging**. |
| **Über (Hashpoint <version>)** | Loggt Versionsinformationen. (Kein Dialog – Details siehe Tab **Über** im Hauptfenster.) |
| **Hilfe** | Öffnet das Hauptfenster und wechselt direkt in den Tab **Hilfe** mit dem eingebetteten Benutzerhandbuch (siehe Abschnitt **Hilfe-Tab**). |
| **Beenden** | Schließt das Programm vollständig. Offene Process-Tracks und Tag-Blöcke werden vorher sauber beendet. Es findet **kein** Sync mehr beim Beenden statt — die Synchronisation läuft beim nächsten Start der Anwendung, siehe Abschnitt **Sync beim Starten**. |

## Manuelles Tagging

> 💡 **Tipp:** Für blitzschnelles Wechseln zwischen häufig benutzten Tags — auch von anderen Anwendungen aus — gibt es zusätzlich den globalen [Quick-Tag-Picker](quick-tag.md) (Default-Hotkey `Strg+Alt+T`).

Das Submenü **Manueller Tag** ist die schnellste Möglichkeit, eine laufende Tätigkeit explizit einer Kategorie zuzuordnen — z. B. einem Telefonat, einem Meeting oder einer Pause. Anders als der Drag-to-tag-Workflow auf der Zeitachse erzeugt es eine **offene Sitzung ohne festes Ende**: alles, was Sie in dieser Zeit tun, wird dem gewählten Tag zugeschrieben, bis Sie die Sitzung explizit beenden.

### Sitzung starten

- Jeder konfigurierte Tag erscheint als eigener Eintrag im Submenü (Stand bei Tray-Start — neu angelegte Tags benötigen einen Anwendungs-Neustart, um aufzutauchen).
- Die Einträge sind **nach Eltern-Tag gruppiert**: zuerst der Eltern-Tag, direkt danach dessen Sub-Tags. Sub-Tags erscheinen mit dem Eltern-Namen als Präfix (`#projekta › #frontend`), damit gleichnamige Sub-Tags unterschiedlicher Eltern unterscheidbar bleiben.
- **Klick auf einen Tag** öffnet einen manuellen Tag-Block ab „jetzt" (gerundet auf das Granularitätsraster).
- War bereits ein anderer manueller Tag offen, wird er sauber geschlossen, bevor der neue beginnt.

### Auto-Tag-Unterbrechung & automatische Fortsetzung

Während eine offene manuelle Sitzung läuft, kann das Auto-Tagging eingreifen:

1. Sie starten manuell `#code` um 09:00.
2. Um 10:30 öffnen Sie einen Browser, der die Auto-Regel `#web` auslöst.
3. Der TimeTracker schließt den manuellen Block bei 10:30 und öffnet einen Auto-Block `#web` ab 10:30 (beide auf das Raster gesnappt).
4. Sobald Sie wieder zu einem nicht-`#web`-Programm wechseln (oder idle gehen), endet der Auto-Block.
5. Der TimeTracker öffnet **automatisch** einen neuen manuellen Block mit dem ursprünglichen Tag (`#code`) und derselben Beschreibung wie vorher.

So bleibt eine manuelle „Hintergrund-Kategorisierung" über mehrere Auto-Tag-Phasen hinweg erhalten — Sie müssen den Tag nicht jedes Mal neu setzen.

### Sitzung während aktiver Auto-Phase starten

Klicken Sie auf einen manuellen Tag, während gerade ein Auto-Block läuft, wird **kein** neuer manueller Block sofort erzeugt. Stattdessen merkt sich der TimeTracker Ihren Wunsch — sobald der Auto-Block endet, beginnt der manuelle Block automatisch.

### Sitzung beenden

- **Kein Tag (Stop)** beendet die offene manuelle Sitzung sofort. Auch eine pausierte (während Auto-Phase wartende) Sitzung wird damit verworfen.
- Erneutes Klicken ohne aktiven Block ist folgenlos.

### Beim Anwendungsstart

Falls beim Beenden des Programms eine offene manuelle Sitzung nicht gestoppt wurde, wird sie beim nächsten Start automatisch geschlossen — entweder am Ende des letzten Process-Tracks (wenn Tracking lief) oder an der aktuellen Uhrzeit. So gibt es nie eine „Geister-Sitzung", die unbemerkt weiter Zeit aufschreibt.

### Zusammenspiel mit „Pause Tracking"

| Zustand | Verhalten |
| --- | --- |
| Tracking aktiv + manuell offen | Manueller Block deckt die Zeit ab; Auto-Tags unterbrechen ihn temporär (siehe oben). |
| Tracking pausiert + manuell offen | Manueller Block läuft weiter, aber Auto-Tags greifen nicht (es werden keine Process-Tracks erfasst). Der manuelle Block deckt die ganze Pause-Zeit ab. |
| Tracking aktiv + kein manuell offen | Nur Auto-Tags greifen. |

## Hilfe-Tab

Der Tray-Eintrag **Hilfe** ist die schnellste Abkürzung in das eingebettete Benutzerhandbuch:

- Hauptfenster wird in den Vordergrund geholt (auch wenn es minimiert oder versteckt war).
- Der Tab **Hilfe** wird automatisch ausgewählt.
- Links eine Sidebar mit allen Kapiteln (gleiche Reihenfolge wie in der [Übersicht](README.md)), rechts der Inhalt der gewählten Seite.
- Interne Links zwischen Doku-Seiten (`tags.md`, `personio.md`, …) funktionieren ohne Browser — Klick wechselt einfach das Sidebar-Item.
- Die Inhalte sind **in den Binary eingebettet** (`go:embed docs/user/*.md`); die Hilfe funktioniert offline und passt 1:1 zur installierten Version.

Sie können die Hilfe jederzeit auch ohne Tray-Klick erreichen, indem Sie im Hauptfenster den Tab **Hilfe** aufrufen.

## Pause vs. Beenden

| Aktion | Wirkung auf Erfassung | Wirkung auf Anwendung |
| --- | --- | --- |
| Hauptfenster schließen (X) | Läuft weiter | Bleibt im Tray |
| **Pause Tracking** | Pausiert Process-Tracking + Auto-Tags | Bleibt im Tray |
| **Beenden** | Offene Tracks + Tag-Blöcke werden geschlossen, **kein** Sync | Anwendung wird vollständig beendet |

## Sync beim Starten

Beim **Starten** der Anwendung sucht Hashpoint automatisch den letzten
Kalendertag vor heute, der noch unsynchronisierte Tag-Blöcke enthält, und
überträgt ihn an Personio. So wird die Erfassung des vorherigen
Arbeitstages garantiert in Personio gebucht — auch wenn das System am
Vorabend hart heruntergefahren wurde.

1. Hashpoint öffnet das Hauptfenster sofort. Der Sync läuft im Hintergrund.
2. Es wird der jüngste Tag mit `synced_at IS NULL` gesucht (also: alle
   Blöcke des Tages sind noch nicht in Personio). Wochenenden und Urlaub
   werden automatisch übersprungen — gefunden wird der letzte echte
   Arbeitstag.
3. Sind alle vorherigen Tage bereits gesynct, läuft kein Sync und es
   erscheint kein Banner.
4. Ergebnis (Erfolg, Teilfehler, Hard-Fail) erscheint als dismissbarer
   Banner oben im Hauptfenster. Erfolg = grün, Fehler = rot.
5. Die Übertragung ist auf 30 s gedeckelt; bei Netz- oder Personio-Problemen
   wird der Sync abgebrochen und der Banner zeigt den Fehler.
6. Ist (noch) keine Personio-Sitzung hinterlegt, läuft kein Sync und es
   erscheint kein Banner — die Anwendung startet ganz normal.

> **Was, wenn die App dauernd läuft?** Der Sync prüft beim Start, was es
> zu tun gibt — solange Sie die Anwendung nicht neu starten, passiert
> nichts. Den **heutigen** Tag synchronisieren Sie wie gewohnt manuell
> über *Sync zu Personio* in der Zeitachse oder im Tray.

> **Hard-Kill (Task-Manager, Stromausfall):** Beim nächsten Start schließt
> Hashpoint automatisch noch offene Blöcke (`Recover`) und der Startup-Sync
> übernimmt sie dann.

## Wenn das Tray-Icon fehlt

Falls das Hashpoint-Icon nicht im Benachrichtigungsbereich sichtbar ist:

1. Auf den **Pfeil nach oben** (`˄`) im Tray klicken – das Icon kann in den Overflow-Bereich verschoben sein.
2. **Windows-Einstellungen → Personalisierung → Taskleiste → Symbole, die in der Taskleiste angezeigt werden** öffnen.
3. Eintrag *Hashpoint TimeTracker* auf **An** setzen.

Falls die Anwendung gar nicht läuft, im Startmenü erneut starten.

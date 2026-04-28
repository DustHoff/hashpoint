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
| **Pause Tracking** *(Checkbox)* | Hält das automatische Fokus-Tracking an oder setzt es fort. Häkchen = pausiert. Beim Pausieren wird der laufende Programm-Block sofort sauber abgeschlossen. Manuelles Tagging (siehe unten) bleibt unabhängig davon möglich. |
| **Sync zu Personio (heute)** | Synchronisiert den heutigen Tag direkt an Personio, ohne das Hauptfenster zu öffnen. |
| **Manueller Tag** *(Submenü)* | Ordnet die laufende Zeit explizit einem Tag zu. Siehe Abschnitt **Manuelles Tagging**. |
| **Autostart** *(Checkbox)* | Aktiviert/deaktiviert den automatischen Start beim Windows-Login. Schreibt/entfernt den entsprechenden Registry-Eintrag. |
| **Über (Hashpoint <version>)** | Loggt Versionsinformationen. (Kein Dialog – Details siehe Tab **Über** im Hauptfenster.) |
| **Beenden** | Schließt das Programm vollständig. Offene Blöcke werden vorher sauber beendet. |

## Manuelles Tagging

Das Submenü **Manueller Tag** ist die schnellste Möglichkeit, laufende Zeit
explizit einer Kategorie zuzuordnen — z. B. einem Telefonat, einem Meeting
ohne Bildschirmaktivität oder einer Pause:

- Jeder konfigurierte Tag erscheint als eigener Eintrag im Submenü
  (Stand bei Tray-Start — neu angelegte Tags benötigen einen
  Anwendungs-Neustart, um aufzutauchen).
- **Klick auf einen Tag** öffnet einen Manual-Block ab „jetzt" mit dem
  gewählten Tag. War bereits ein Manual-Block offen, wird er beim Wechsel
  sauber geschlossen, bevor der neue beginnt.
- **Kein Tag (Stop)** beendet den aktuell offenen Manual-Block. Erneutes
  Klicken ohne aktiven Block ist folgenlos.

> **Zusammenspiel mit dem Tracking:** Das Fokus-Polling läuft auch während
> einer manuellen Sitzung weiter, **wenn** „Pause Tracking" inaktiv ist.
> Neu erfasste Programm-Blöcke übernehmen dann automatisch den manuellen
> Tag (statt durch die Auto-Tagging-Regeln zu laufen). Ist „Pause Tracking"
> aktiv, bleibt nur der Platzhalter-Block des manuellen Tags — keine
> Programm-Blöcke. Manuelles Tagging funktioniert in beiden Zuständen.

## Pause vs. Beenden

| Aktion | Wirkung auf Erfassung | Wirkung auf Anwendung |
| --- | --- | --- |
| Hauptfenster schließen (X) | Läuft weiter | Bleibt im Tray |
| **Pause Tracking** | Pausiert | Bleibt im Tray |
| **Beenden** | Pausiert + offener Block geschlossen | Anwendung wird vollständig beendet |

## Wenn das Tray-Icon fehlt

Falls das Hashpoint-Icon nicht im Benachrichtigungsbereich sichtbar ist:

1. Auf den **Pfeil nach oben** (`˄`) im Tray klicken – das Icon kann in den Overflow-Bereich verschoben sein.
2. **Windows-Einstellungen → Personalisierung → Taskleiste → Symbole, die in der Taskleiste angezeigt werden** öffnen.
3. Eintrag *Hashpoint TimeTracker* auf **An** setzen.

Falls die Anwendung gar nicht läuft, im Startmenü erneut starten.

# ADR 0012 – Tray-zentrierte Bedienung, Autostart nur über den Installer

- **Status:** Akzeptiert (retrospektiv dokumentiert)
- **Datum:** 2026-09-28
- **Bezug:** Spec §2.2, §5.2; `cmd/timetracker/tray_*.go`; `docs/user/tray.md`

## Kontext

Zeiterfassung läuft den ganzen Tag im Hintergrund; ein permanent offenes Fenster stört. Häufige Aktionen (Pause, Sync, Tag wechseln) müssen ohne Hauptfenster erreichbar sein.

## Entscheidung

- Bedienung primär über ein **System-Tray-Icon** (`fyne.io/systray`): Linksklick öffnet das Hauptfenster; Kontextmenü mit Öffnen, **Pause Tracking**, **Sync zu Personio (heute)**, **Manueller Tag** (Eltern-zuerst-Submenü inkl. „Kein Tag (Stop)“), Über, **Hilfe**, **Beenden**.
- Fenster-X schließt nur die UI; **Beenden** schließt offene Tracks/Blöcke sauber, synchronisiert aber **nicht** (Startup-Sync, ADR 0011).
- Tray lebt im **Collector** (ADR 0001) und überlebt damit das Schließen/Abstürzen der UI. Bleibt die Tray-Initialisierung aus (z. B. nach Modern Standby), beendet sich der Collector nach 8 s und wird vom Watchdog neu gestartet (ADR 0022).
- **Autostart** ausschließlich per MSI (`HKCU\…\Run\HashpointTimeTracker`); kein In-App-Toggle.

## Konsequenzen

- ✅ Minimale Störung im Arbeitsalltag; Kernaktionen mit zwei Klicks.
- ✅ Eine einzige, nachvollziehbare Autostart-Quelle.
- ⚠️ Neue Tags erscheinen im Tray-Submenü erst nach Neustart.
- ⚠️ Autostart für weitere Profile/Portable-EXE nur manuell.

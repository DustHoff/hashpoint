# ADR 0022 – Resilienz: Crashguard, Single-Instance, Power-Monitor und Watchdog-Dienst

- **Status:** Akzeptiert (retrospektiv dokumentiert)
- **Datum:** 2026-09-28
- **Bezug:** ADR 0001 (Stufe A/C), Issue #21; `internal/{crashguard,watchdog,uisupervisor}`, `cmd/timetracker/watchdog_windows.go`, `internal/winapi/{power,singleinstance}_windows.go`

## Kontext

Hashpoint wurde rund um Modern Standby/Hibernate ohne Userspace-Callback beendet; Tracking fiel stundenlang aus, ohne Spur im Log. Mehrfachstarts führten zu DB-Races.

## Entscheidung

- **Crashguard:** `Recover`/`Safe` an allen Goroutine-Einstiegspunkten loggen Panics mit Stack und halten den Prozess am Leben; `RecoverFatal` am Prozess-Einstieg. **Marker-Dateien** pro Prozessrolle machen unsaubere Beendigungen beim nächsten Start sichtbar.
- **Single-Instance** per session-lokalem Mutex `Hashpoint.SingleInstance`.
- **Power-Monitor:** schließt Tracks an der Suspend-Kante und setzt nach Resume fort (statt 5-min-Heuristik).
- **Heartbeat** `last_seen` begrenzt Verlust auf ein Poll-Intervall (ADR 0003).
- **UI-Supervisor:** der Collector spawnt, respawnt und beendet den UI-Prozess.
- **Watchdog als Windows-Dienst** (`HashpointWatchdog`, LocalSystem, per MSI installiert, gleiches Binary mit `--watchdog`): pollt die Collector-Lebendigkeit, schließt bei unsauberem Tod offene Tag-Blöcke auf den letzten bestätigten Zeitpunkt und **startet den Collector in der User-Session neu** – erst nach dem Entsperren. Crash-Loop-Cooldown verhindert Neustart-Stürme.

## Konsequenzen

- ✅ Jeder Absturz hinterlässt eine Spur; Datenverlust stark begrenzt; Selbstheilung nach Standby.
- ⚠️ Ein Dienst mit LocalSystem-Rechten erweitert die Installations-Anforderungen (Admin/MSI).
- ⚠️ Mehr bewegliche Teile (drei Prozessrollen: Collector, UI, Watchdog).

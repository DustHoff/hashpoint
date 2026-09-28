# ADR 0003 – Fokus-Tracking per Polling mit Idle-, Lock- und Suspend-Behandlung

- **Status:** Akzeptiert (retrospektiv dokumentiert)
- **Datum:** 2026-09-28
- **Bezug:** Spec §2.1, §4.4; `internal/tracker`, `internal/winapi`; Migration 0010

## Kontext

Kernfunktion ist die automatische Erfassung, welches Fenster im Vordergrund ist. Windows bietet dafür keine zuverlässige, vollständige Event-Quelle über alle Prozesse; zugleich soll die CPU-Last im Leerlauf < 1 % bleiben und Fokuswechsel in < 3 s erfasst werden.

## Entscheidung

- **Polling** in **einer** dedizierten Goroutine (Default 2 s, `tracking.poll_interval_sec`) über `GetForegroundWindow`, `GetWindowThreadProcessId`, `GetWindowTextW`. Alle Win32-Aufrufe sind in `internal/winapi` gekapselt.
- Gleicher Prozess + gleicher Titel ⇒ derselbe `process_track`; jede Änderung schließt den alten und öffnet einen neuen Track (Fokus-Tracks sind dadurch **disjunkt per Konstruktion**).
- **Idle-Detection** via `GetLastInputInfo` (Default 5 min): Track wird beendet, Orchestrator erhält `OnFocusCleared`.
- **Lock/Suspend:** Ein Power-Monitor schließt offene Tracks an der Suspend-Kante und setzt das Tracking nach Resume fort.
- **Crash-Recovery:** Beim Start werden **alle** offenen Tracks geschlossen – auf den persistierten **`last_seen`-Heartbeat** (pro aktivem Poll-Tick geschrieben, monoton), sonst Fallback `min(start + idle_threshold, now, next_open.start)`.
- Der Tracker trifft **keine Tag-Entscheidungen**; er meldet Fokuswechsel an den Tagging-Orchestrator (ADR 0007).
- `tracking.enabled = false` pausiert Polling und Auto-Tagging (Tray „Pause Tracking“).

## Konsequenzen

- ✅ Einfach, robust, plattformnah testbar; sekundengenaue Rohdaten.
- ✅ Maximaler Datenverlust bei hartem Prozess-Tod ≈ ein Poll-Intervall.
- ⚠️ Browser-Tabs nur über den Fenstertitel erkennbar (kein DOM-Zugriff) – akzeptiert.
- ⚠️ Ein `UPDATE` pro Tick für den Heartbeat (vernachlässigbar).

## Alternativen

- `SetWinEventHook` (EVENT_SYSTEM_FOREGROUND) – liefert keine Titeländerungen innerhalb eines Fensters und keine Idle-Information; Polling wäre ohnehin nötig.

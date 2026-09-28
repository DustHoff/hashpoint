# ADR 0002 – Lokale Datenhaltung in SQLite (pure Go)

- **Status:** Akzeptiert (retrospektiv dokumentiert)
- **Datum:** 2026-09-28
- **Bezug:** Spec §3, §4.3; CLAUDE.md §4, §12

## Kontext

Hashpoint erfasst sehr feingranulare, potenziell sensible Aktivitätsdaten (Prozessnamen, Fenstertitel). Die Daten müssen offline verfügbar, pro Windows-User getrennt und crash-sicher sein. Der Build soll ohne C-Toolchain reproduzierbar bleiben.

## Entscheidung

- Persistenz in einer **lokalen SQLite-Datei** unter `%LOCALAPPDATA%\TimeTracker\data.db` (Pfad immer aus `config`, nie hartkodiert).
- Treiber **`modernc.org/sqlite`** (pure Go, **kein CGO**).
- Schema ausschließlich über **sequenziell nummerierte Up-/Down-Migrationen** (`internal/storage/migrations`, aktuell 0001–0010); keine Schema-Änderung im Code.
- **Repository-Pattern**: ein `XxxRepo` pro Tabelle mit Interface in `storage/interfaces.go`; SQL als Konstanten, Prepared Statements im Tracking-Loop, Transaktionen bei Multi-Tabellen-Operationen.
- Alle Zeitstempel in **UTC**; Umrechnung in Lokalzeit erst in UI bzw. Sync.
- Nach dem Prozess-Split (ADR 0001) ist der **Collector alleiniger DB-Owner**.

## Konsequenzen

- ✅ Keine Server-Infrastruktur, keine Daten verlassen das Gerät (außer bewusst per Sync).
- ✅ Pure-Go-Build, Tests über In-Memory-SQLite.
- ✅ Pro Windows-Profil eigene DB (Multi-User-fähig ohne Zusatzlogik).
- ⚠️ Kein geräteübergreifender Datenabgleich; Backup liegt in Anwenderhand.
- ⚠️ Jede Schemaänderung braucht zwingend eine Down-Migration.

## Alternativen

- `mattn/go-sqlite3` – schneller, aber CGO → verworfen.
- Serverseitige DB / Cloud-Sync – widerspricht Datenschutzanforderung „alles lokal“.

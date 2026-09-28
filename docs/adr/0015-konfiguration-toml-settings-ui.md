# ADR 0015 – Konfiguration als TOML mit vollständiger Settings-UI und Hot-Reload

- **Status:** Akzeptiert (retrospektiv dokumentiert)
- **Datum:** 2026-09-28
- **Bezug:** CLAUDE.md §6, §9a; Spec §4.6; `internal/config`, `Settings.tsx`

## Kontext

Endanwender sollen keine Dateien editieren müssen, trotzdem soll die Konfiguration für IT-Rollouts und Support lesbar bleiben. Secrets dürfen nicht in Klartext-Dateien landen.

## Entscheidung

- Persistenz als **TOML** in `%APPDATA%\TimeTracker\config.toml` (`BurntSushi/toml`); Defaults zentral in `config/defaults.go`; Validierung beim Laden und Speichern.
- **Jedes Config-Feld muss über die Settings-UI bearbeitbar sein** (Typ in `types.ts`, Input + Hilfetext in `Settings.tsx`, identische Defaults). TOML ist Persistenz, kein User-Interface.
- Bereiche: Erfassung, Arbeitszeit, Quick-Tag, Kommunikation, Personio (Tenant), Entra ID (Client-/Tenant-ID), Rufbereitschaft.
- **Live-Reconfig** über `OnConfigSet` (z. B. Kommunikationslisten, Hotkey, Tracking-Schalter) ohne Neustart.
- **Keine Secrets in der Config**: Personio-Session/GitHub-Token → Credential Manager, Entra-Tokens → DPAPI-Datei, Plugin-Passwörter → DPAPI in der DB.
- Plugin-Einstellungen sind ausgenommen (eigene Tabelle, manifest-generierte UI, ADR 0016).

## Konsequenzen

- ✅ Keine „versteckten“ Optionen; IT kann trotzdem Dateien vorbereiten.
- ✅ Klare Trennung Konfiguration vs. Geheimnisse.
- ⚠️ Jedes neue Feld verursacht Frontend-Aufwand (bewusst).

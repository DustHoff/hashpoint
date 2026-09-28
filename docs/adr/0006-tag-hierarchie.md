# ADR 0006 – Zweistufige Tag-Hierarchie mit Hashtag-Namensschema

- **Status:** Akzeptiert (retrospektiv dokumentiert)
- **Datum:** 2026-09-28
- **Bezug:** Spec §2.4; `internal/tagging`, `storage/tag_repo.go`; Migration 0009 (`order_name`)

## Kontext

Zeiten müssen Projekten und Tätigkeiten zugeordnet und nach Personio übertragen werden. Eine beliebig tiefe Hierarchie wäre schwer bedienbar (Tray-Menü, Quick-Picker) und lässt sich nicht sinnvoll auf Personio-Projekte abbilden.

## Entscheidung

- Tags in **genau zwei Ebenen**: Parent-Tag (Projekt) und Sub-Tag (Tätigkeit). Ein Tag-Block trägt genau **einen** Tag (Parent oder Sub).
- **Namensschema** `^#[A-Za-z0-9]+$` (DB-`CHECK` + Validierung; fehlendes `#` wird ergänzt); Eindeutigkeit pro Parent.
- Pro Tag: Farbe, optionale Beschreibung, `personio_project_id`, `sync_to_personio`, optionaler **Auftrag** (`order_name`, nur lokal, nicht synchronisiert).
- **Mapping-Vererbung:** Sub-Tags ohne eigenes Personio-Mapping erben das des Parents.
- UI-Darstellung überall **Eltern-zuerst gruppiert**, Sub-Tags mit Präfix `<Parent> › <Sub>`.
- `EnsureByPath` (find-or-create, case-insensitiv) ist der einzige Weg für automatisch angelegte Tags (Personio-Import, `tag_provider`-Plugins); **bestehende User-Tags werden nie überschrieben**.
- `personio_activity_id` bleibt als Legacy-Feld erhalten, wird aber nicht genutzt.

## Konsequenzen

- ✅ Einfache Bedienung, eindeutiger Kommentar-Aufbau `#Parent #Sub — Beschreibung` für Personio (und Rück-Parsing beim Import).
- ✅ Hashtags sind maschinenlesbar und kollisionsarm.
- ⚠️ Keine tieferen Strukturen; Sonderzeichen/Leerzeichen in Namen nicht möglich.

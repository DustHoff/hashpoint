# ADR 0019 – Rufbereitschafts-Dokumentation mit Off-Hours-Timeline und Plugin-Distribution

- **Status:** Akzeptiert (retrospektiv dokumentiert)
- **Datum:** 2026-09-28
- **Bezug:** Spec §2.6; `internal/plugin/oncall`, `internal/app/oncall.go`, `OnCall.tsx`; Migration 0007; `docs/plugins/capability-{off-hours-provider,oncall-documentation}.md`

## Kontext

Einsätze in der Rufbereitschaft außerhalb der Arbeitszeit müssen in externen Systemen (Ticketsysteme) dokumentiert werden. Welche Systeme das sind, ist organisationsspezifisch; Feiertage und Sonderschichten sind dynamisch.

## Entscheidung

- Ein Tag-Block **qualifiziert**, wenn er geschlossen ist, die **Off-Hours-Timeline** schneidet und sein Tag (oder ein Vorfahr) in `oncall.tag_ids` steht. Leere Liste ⇒ Feature dormant.
- Off-Hours-Basis aus `[work_schedule]` (`work_days`, `start_hour..end_hour`); **`off_hours_provider`-Plugins** liefern `add`-/`remove`-Intervalle (Union der adds, danach **remove gewinnt global**). Pull-basiert, In-Memory-Cache pro Plugin und Jahr, kein DB-Cache, kein Backfill.
- Nach jeder Block-Mutation läuft `Recheck`: neuer Treffer ⇒ Doc `draft`; verlorene Qualifikation ⇒ `stale` (nie automatisch gelöscht). Eingereichte Docs sind historisch unveränderlich.
- Formular: betroffene Anwendung, Art (`planned_maintenance`|`service_disruption`), Lösung.
- **Hashpoint pusht selbst nirgendwohin**: Senden verteilt parallel an alle laufenden `oncall_documentation`-Plugins; Status `submitted`/`partial`/`failed`, Retry nur an gescheiterte Plugins.

## Konsequenzen

- ✅ Keine vergessenen Einsätze; Kern bleibt zielsystemneutral.
- ✅ Feiertage/Sonderschichten ohne Kernänderung.
- ⚠️ Ohne passendes Plugin bleibt die Doku im Entwurf.
- ⚠️ Neu aktivierte Provider wirken nur auf künftig mutierte Blöcke.

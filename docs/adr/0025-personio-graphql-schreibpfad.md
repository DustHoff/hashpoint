# ADR 0025 – Personio-Schreibpfad über GraphQL (Attendance-Migration)

- **Status:** Akzeptiert
- **Datum:** 2026-10-16
- **Bezug:** Spec §2.5.2, §2.5.4; CLAUDE.md §8; ADR 0010, 0011; `internal/personio/track.go`, `docs/technical/personio-api.md`

## Kontext

Ab dem 16.10.2026 schlug jeder Sync mit `personio: set day: status 501:` fehl. Der REST-Endpunkt `PUT /svc/attendance-api/v1/days/{day_id}` antwortet mit `501 Not Implemented` und leerem Body. Ein HAR-Capture der Personio-Web-App zeigt: Personio hat die Attendance-Daten migriert (`TM_GetAttendanceSettingsMigration` → `MIGRATION_STATE_CONSUMPTION_ENABLED`), und die Web-App speichert Tage nur noch über die GraphQL-Mutation `TM_TrackAttendancePeriodsOnDay_v2026071301`. Das Timesheet-`GET` ist unverändert.

## Entscheidung

- Schreiben ausschließlich über `POST /graphql?op=TM_TrackAttendancePeriodsOnDay_<version>`. Der Request ist identisch mit dem der Web-App aufgebaut: `autoFix=false`, `origin=ATTENDANCE_ORIGIN_WEB`, der Tag wird über `assumedAttributionDate` adressiert, die Perioden haben strukturierte lokale Zeiten.
- Die Mutation ist eine **Persisted Query**: Operationsname und Dokument-Hash stehen als Konstanten in `track.go`. Kennt Personio die Operation nicht mehr, liefert der Client `ErrOperationOutdated`. Das bedeutet „Code-Update nötig“, nicht „erneut anmelden“.
- GraphQL-`errors` werden auch bei HTTP `200` als Fehlschlag gewertet; ebenso eine Antwort ohne `saved`.
- Kein Fallback auf den REST-Endpunkt: er ist für migrierte Tenants tot. Ein Tenant-abhängiges Umschalten wäre ungetesteter Code.
- `personio_id` an synchronisierten Blöcken bleibt die `day_id` aus dem Timesheet. Ohne Personio-Datensatz wird stattdessen das Datum gespeichert, weil keine clientseitige Day-UUID mehr erzeugt wird.

## Konsequenzen

- ✅ Sync funktioniert wieder; Lesepfad, Preflight und Import sind unverändert.
- ✅ Klare Fehlermeldung, wenn Personio die Operation erneut versioniert.
- ⚠️ Hash und Versionssuffix veralten bei Personio-Releases. Aktualisierung: HAR-Capture beim Speichern eines Tages, `query` und `operationName` übernehmen.
- ⚠️ Das Format von `projectId` (`{"id":"…"}`) ist aus dem ID-Schema der API abgeleitet und noch nicht per Capture mit Projekt bestätigt.

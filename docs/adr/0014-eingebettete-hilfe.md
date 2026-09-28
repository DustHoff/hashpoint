# ADR 0014 – Eingebettetes Offline-Benutzerhandbuch (Hilfe-Tab)

- **Status:** Akzeptiert (retrospektiv dokumentiert)
- **Datum:** 2026-09-28
- **Bezug:** Spec §2.3a; `userdocs.go`, `Help.tsx`, `internal/app` (`helpPageOrder`)

## Kontext

Anwender brauchen versionsgenaue Hilfe, auch ohne Netzzugang oder Zugriff auf GitHub. Eine separate Online-Doku driftet von der installierten Version weg.

## Entscheidung

- `docs/user/*.md` wird per `//go:embed` in das Binary eingebettet (`userdocs.go` am Modul-Root).
- Bindings `ListUserDocs`, `GetUserDoc(slug)` (Slug gegen Whitelist `helpPageOrder`, kein Pfad-Traversal), `OpenHelpTab` (Tray-Eintrag „Hilfe“).
- Rendering mit `react-markdown` + `remark-gfm`; interne `*.md`-Links navigieren in der Sidebar.
- Neue Seiten erscheinen erst nach Eintrag in `helpPageOrder` (bewusster Zwei-Schritt).

## Konsequenzen

- ✅ Hilfe passt immer zur installierten Version, funktioniert offline.
- ✅ Doku wird im selben PR wie das Feature gepflegt (DoD CLAUDE.md §14).
- ⚠️ Binary-Größe wächst geringfügig; Doku-Änderungen erfordern ein Release.

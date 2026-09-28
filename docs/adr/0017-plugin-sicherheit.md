# ADR 0017 – Plugin-Sicherheit: Approval-Gate, DPAPI-Secrets und Secret-Handles

- **Status:** Akzeptiert (retrospektiv dokumentiert)
- **Datum:** 2026-09-28
- **Bezug:** `internal/plugin/{validate,hostapi,handles}.go`, `storage/plugin_{approval,settings}_repo.go`, `storage/plugin_cipher_*.go`; `docs/plugins/README.md`

## Kontext

Plugins erhalten Zugriff auf die Personio-Session, Entra-Tokens und Plugin-Secrets. Ein beliebiges Verzeichnis im Plugin-Ordner (Side-Load, Malware-Drop) darf nicht automatisch mit Benutzerrechten ausgeführt werden.

## Entscheidung

- **Approval-Gate (opt-in):** Neue Plugins stehen auf `pending_approval` und werden erst nach Klick „Plugin genehmigen und starten“ ausgeführt. Automatisch genehmigt: MSI-Seed und Installationen über den Katalog. Persistenz in `settings` (`plugins.approved`).
- Plugin-Namen werden als sicherer Einzel-Pfadbestandteil validiert (kein `..`, keine Separatoren/Laufwerke).
- `password`-Felder werden **DPAPI-verschlüsselt** (CurrentUser) in der DB gespeichert; das Plugin erhält nur ein **opakes `SecretHandle`**, das es per `RedeemSecret` einlöst. Handles verfallen bei Crash, Reload und Host-Neustart.
- HostAPI ist bewusst **„alles oder nichts“** (keine Scope-Allowlist); Sentinel-Errors (`ErrPersonioNotAvailable`, `ErrEntraNotAvailable`) signalisieren „Feature aus“.
- Der Host loggt Tokens/Cookies nie; das SDK verpflichtet Autoren, sie nicht zu persistieren.

## Konsequenzen

- ✅ Kein Auto-Exec unbekannter Binaries; Secrets sind geräte- und benutzergebunden.
- ⚠️ **Kein Sandboxing** – ein genehmigtes Plugin ist vollständig vertrauenswürdig. Dies ist Containment, keine Isolation.

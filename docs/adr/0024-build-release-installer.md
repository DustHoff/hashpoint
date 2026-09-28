# ADR 0024 – Reproduzierbarer Build, Auto-Versionierung und MSI-Installer

- **Status:** Akzeptiert (retrospektiv dokumentiert)
- **Datum:** 2026-09-28
- **Bezug:** `docs/github-spec.md`; Spec §5; `.github/workflows/*`, `build/wix/hashpoint.wxs`, `winres/`

## Kontext

Releases sollen ohne manuelle Schritte, nachvollziehbar und verifizierbar entstehen. Unternehmen benötigen einen stillen Installer für Rollouts.

## Entscheidung

- **Ausschließlich GitHub Actions** baut und veröffentlicht; `main` ist geschützt (CI: lint, test auf Windows, build).
- **SemVer mit Auto-Patch-Bump** bei jedem Merge auf `main`; Minor/Major per `manual-bump.yml`; `[skip release]`/`[skip ci]` überspringt. Version/Commit/Build-Datum per `ldflags`.
- **Reproduzierbar:** gepinnte Go/Node-Versionen, `-trimpath`, `-buildid=`, commit-abgeleitetes Build-Datum, `go-winres` mit `TimeDateStamp=0`.
- **Ein Binary, mehrere Modi** (`--collector`, `--ui`, `--watchdog`).
- Artefakte: `hashpoint.exe`, **WiX-3.14-MSI** (perMachine, de-DE, stabile UpgradeCode/Komponenten-GUIDs, `MajorUpgrade`, HKCU-Autostart, Plugin-Seed, Watchdog-Dienst) und `checksums.txt` (SHA-256).
- Codesigning über Azure Trusted Signing (OIDC) vorgesehen.

## Konsequenzen

- ✅ Jede Version = ein Tag = ein CI-Run; Hash-Verifikation für Anwender.
- ✅ Stille Installation (`msiexec /quiet`) für IT.
- ⚠️ Unsignierte Builds lösen Defender/SmartScreen-False-Positives aus (README).
- ⚠️ Die stabilen WiX-GUIDs dürfen nie neu generiert werden.

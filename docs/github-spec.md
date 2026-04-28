# github-spec.md – CI/CD & Versioning für Hashpoint

Dieses Dokument beschreibt die GitHub-Actions-Workflows und die Versionierungsstrategie. Verbindlich für alle Beiträge.

---

## 1. Grundregeln

- **Alle Builds, Tests und Releases laufen ausschließlich über GitHub Actions.** Lokale Builds sind nur für Entwicklung erlaubt, nicht für Distribution.
- **Branch `main` ist geschützt:** keine Direct Pushes, nur PR-Merges. Required Checks: `ci / lint`, `ci / test`, `ci / build`.
- **Releases sind reproduzierbar:** jede veröffentlichte Version entspricht genau einem Git-Tag und einem CI-Run.

---

## 2. Semantic Versioning

- Schema: `MAJOR.MINOR.PATCH` gemäß [semver.org](https://semver.org).
- **Startversion: `0.1.0`** (initial einmalig als Tag `v0.1.0` setzen).
- Auto-Bump-Regel:
  - **Jeder Merge / Push auf `main` erhöht automatisch die `PATCH`-Nummer** und erzeugt einen neuen Git-Tag (`vX.Y.Z`).
  - `MINOR`- und `MAJOR`-Bumps werden manuell ausgelöst (siehe §5).
- Tags haben das Präfix `v` (z. B. `v0.1.7`).
- Die aktuelle Version wird per `ldflags` ins Go-Binary eingebettet:
  ```
  -ldflags="-X main.version=${VERSION} -X main.commit=${SHA} -X main.buildDate=${DATE}"
  ```
- Im UI über Wails-Binding `App.Version()` abrufbar; im Tray-Menü unter „Über".

---

## 3. Workflow-Übersicht

```
.github/
└── workflows/
    ├── ci.yml          # PRs + Pushes auf Feature-Branches
    ├── release.yml     # Push auf main → Auto-Patch-Bump + Release
    └── manual-bump.yml # Manuell: minor/major-Release
```

---

## 4. CI-Workflow (`ci.yml`)

**Trigger:** `pull_request` auf alle Branches, `push` auf Branches != `main`.

**Jobs (parallel):**

### `lint`
- Runner: `ubuntu-latest`
- Schritte:
  - `actions/checkout@v4`
  - `actions/setup-go@v5` (Version aus `go.mod`)
  - `golangci/golangci-lint-action@v6` mit Config aus `.golangci.yml`
  - Frontend-Lint: `npm ci && npm run lint` in `frontend/`

### `test`
- Runner: `windows-latest` (Win32-API-Tests laufen nur dort)
- Schritte:
  - Go-Setup, Cache via `actions/cache` für `~/go/pkg/mod` und Build-Cache
  - `go test ./... -race -coverprofile=coverage.out`
  - Coverage-Upload nach Codecov optional
  - Integrationstests: `go test -tags=integration ./test/...`

### `build`
- Runner: `windows-latest`
- Schritte:
  - Go + Node + Wails-CLI installieren (`go install github.com/wailsapp/wails/v2/cmd/wails@latest`)
  - `wails build -clean`
  - Artefakt `hashpoint.exe` als `actions/upload-artifact@v4` für PR-Tests verfügbar machen (Retention 7 Tage)

---

## 5. Release-Workflow (`release.yml`)

**Trigger:** `push` auf `main` (also nach jedem PR-Merge).

**Jobs:**

### `bump-tag`
- Runner: `ubuntu-latest`
- Schritte:
  - `actions/checkout@v4` mit `fetch-depth: 0` (für Tag-Historie)
  - `mathieudutour/github-tag-action@v6.2` mit:
    - `default_bump: patch`
    - `tag_prefix: v`
    - `release_branches: main`
  - Output: neuer Tag `vX.Y.Z`

### `build-release`
- Runner: `windows-latest`
- `needs: bump-tag`
- Schritte:
  - Checkout auf den neuen Tag
  - Go + Node + Wails installieren
  - Build mit eingebetteter Version:
    ```bash
    wails build -clean -ldflags "-X main.version=${{ needs.bump-tag.outputs.new_tag }} -X main.commit=${{ github.sha }}"
    ```
  - Optional: Inno-Setup-Installer bauen
  - SHA-256-Checksums erzeugen

### `publish`
- `needs: build-release`
- `softprops/action-gh-release@v2`:
  - Tag = `${{ needs.bump-tag.outputs.new_tag }}`
  - Auto-generierte Release-Notes aus Commits seit letztem Tag
  - Assets: `hashpoint.exe`, optional `hashpoint-setup.exe`, `checksums.txt`

---

## 6. Manueller Minor/Major-Bump (`manual-bump.yml`)

- Trigger: `workflow_dispatch` mit Input `bump_type` (`minor` | `major`).
- Setzt manuell ein neues Tag, danach läuft der reguläre Release-Pfad.
- Anwendungsfall: neue Feature-Phase (`minor`), Breaking Change (`major`).

---

## 7. Skip-Konventionen

- Commits mit `[skip ci]` oder `[skip release]` im Subject **bumpen kein Tag** (z. B. reine Doku-Änderungen, die nicht released werden müssen).
- `chore:` und `docs:` Commits werden trotzdem standardmäßig gebumpt — bewusste Entscheidung für linear wachsende Versionen.

---

## 8. Secrets & Permissions

- `GITHUB_TOKEN` reicht für Tag- und Release-Erstellung; explizit als `permissions: contents: write` im Workflow setzen.
- Keine externen Secrets nötig für CI/Release. Personio-Credentials sind ausschließlich Runtime-Config beim Endnutzer, **nie** in Workflows.
- **Codesigning** läuft über **Azure Trusted Signing** (kein PFX mehr). Eine `metadata.json` (`Endpoint`, `CodeSigningAccountName`, `CertificateProfileName`) wird lokal/CI-seitig erzeugt und ist via `.gitignore` ausgeschlossen — die Werte sind deployment-spezifisch und gehören nicht ins Repo. Für CI: Azure-Login per OIDC (Federated Credential auf das Workflow-OIDC-Token), anschließend signiert `signtool sign /dlib Azure.CodeSigning.Dlib.dll /dmdf metadata.json …` das Release-Binary. Erforderliche Tenant-/Subscription-/Account-Werte als Repository-Secrets (`AZURE_TENANT_ID`, `AZURE_CLIENT_ID`, `AZURE_SUBSCRIPTION_ID`).

---

## 9. Caching & Performance

- Go-Module-Cache via `actions/setup-go@v5` (eingebaut).
- Node-Cache via `actions/setup-node@v4` mit `cache: 'npm'` und `cache-dependency-path: frontend/package-lock.json`.
- Wails-Build-Cache: `frontend/dist` zwischen Runs cachen via `actions/cache@v4` mit Key auf `package-lock.json`-Hash.
- Ziel-Laufzeit: CI-Run < 5 Min, Release-Run < 8 Min.

---

## 10. Definition of Done für Workflow-Änderungen

1. Workflow-YAML mit `actionlint` validiert (lokal oder via pre-commit).
2. Auf einem Test-Branch erfolgreich durchgelaufen.
3. Versions-Pins für Actions explizit gesetzt (`@v4`, nicht `@main`).
4. Dokumentiert in dieser Datei.
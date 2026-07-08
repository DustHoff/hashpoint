# GitHub (Feedback)

The in-app **Feedback** tab files issues on GitHub on the user's behalf. Auth is
GitHub's **OAuth Device Flow** (no client secret, no embedded browser). Source:
`internal/feedback/{config,github_client,token_store}.go`, `internal/app/feedback.go`.
The user-facing walkthrough is [`../user/feedback.md`](../user/feedback.md).

> `docs/github-spec.md` documents the CI/CD & release workflow only — **not** this
> runtime OAuth contract.

## Identity & storage

- **GitHub App**, `client_id = "Iv23livhMuISPM3JKmTg"` (`config.go:23`).
- Target repository `DustHoff/hashpoint` (`RepoOwner`/`RepoName`, `config.go:26`).
- Token persisted in the Windows Credential Manager under
  `TimeTracker.GitHubFeedback` (`CredentialTarget`, `config.go:35`) as JSON:
  `access_token`, `refresh_token`, `expires_at`, `refresh_expires_at`,
  `issued_at`, `login`.
- Hosts: `https://github.com` (OAuth), `https://api.github.com` (REST,
  `defaultAPIBaseURL`, `github_client.go:21`).

## Device Flow

All OAuth calls are `POST … application/x-www-form-urlencoded` with
`Accept: application/json`.

1. **Start** — `POST https://github.com/login/device/code` (`github_client.go:178`),
   form `client_id`. Response: `device_code` (kept server-side, never shown),
   `user_code`, `verification_uri`, `expires_in`, `interval` (defaults to 5 s if
   `≤0`).
2. **Poll** — `POST https://github.com/login/oauth/access_token`
   (`github_client.go:226`), form `client_id`, `device_code`,
   `grant_type=urn:ietf:params:oauth:grant-type:device_code`. Handle the `error`
   field: `authorization_pending` (keep polling), `slow_down` (back off by the new
   `interval`), `expired_token` (restart), `access_denied` (user declined). On
   success: `access_token`, `token_type`, `expires_in`, `refresh_token`,
   `refresh_token_expires_in`, `scope`.
3. **Refresh** — `POST …/login/oauth/access_token` (`github_client.go:324`), form
   `client_id`, `grant_type=refresh_token`, `refresh_token`. Triggered ~30 s before
   `expires_at` by `EnsureToken` (`github_client.go:291`).

## REST calls

Every REST request sets (`setGitHubHeaders`, `github_client.go:555`):
`Accept: application/vnd.github+json`, `X-GitHub-Api-Version: 2022-11-28`,
`Authorization: Bearer <access_token>`, `User-Agent: Hashpoint-Feedback/1.0`.

- **Profile** — `GET https://api.github.com/user` → `login`, cached for the status
  display (`fetchLogin`, `github_client.go:374`).
- **Labels** — before creating an issue, each needed label is probed with
  `GET /repos/{owner}/{repo}/labels/{name}` and created with
  `POST /repos/{owner}/{repo}/labels` (`{name,color,description}`) if missing
  (`ensureLabel`, `github_client.go:452`). A **`403` on create is swallowed** — the
  label is marked unavailable and the issue is filed without it. `DefaultLabels`
  (`github_client.go:93`): `bug`, `enhancement`, `question`, `severity:low`,
  `severity:medium`, `severity:high`, `severity:critical`, `user-feedback`.
- **Create issue** — `POST /repos/{owner}/{repo}/issues` with `{title, body,
  labels}` (`CreateIssue`, `github_client.go:510`) → `number`, `html_url`.

## Privacy

The device code and tokens never leave the backend; the frontend only ever sees
the `user_code`, verification URL, link status and the created issue number/URL.
The attached log window is scrubbed of debug entries and window titles before
upload (see [feedback.md](../user/feedback.md)).

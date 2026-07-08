# External service integrations — technical reference

This directory documents **how Hashpoint calls the external services it depends
on**: the exact hosts, endpoints, HTTP methods, headers, request/response bodies,
authentication mechanisms and error signals. It is a developer reference — for
end-user instructions see [`../user/`](../user/README.md), and for the plugin
author API see [`../plugins/`](../plugins/README.md).

The contract descriptions are cross-referenced to `file:line` in the source as of
release `bca601f`. Line numbers drift; **the code is the source of truth** — treat
this as a map, not a spec, and re-check the cited functions when in doubt.

## Services

| Reference | Base host(s) | Auth model | Secret at rest | Source package |
| --- | --- | --- | --- | --- |
| [Personio UI-API](personio-api.md) | `{tenant}.app.personio.com` (API) · `{tenant}.personio.de` (login) | Session cookies + `x-athena-xsrf-token` header | Windows Credential Manager `TimeTracker.PersonioSession` | `internal/personio` |
| [Personio login via CDP](cdp-login.md) | local Chrome, ephemeral debug port | interactive browser login, cookie capture | (produces the Personio session above) | `internal/personio/auth_cdp.go` |
| [GitHub (Feedback)](github-api.md) | `github.com` (OAuth) · `api.github.com` (REST) | OAuth Device Flow → `Authorization: Bearer` | Windows Credential Manager `TimeTracker.GitHubFeedback` | `internal/feedback` |
| [Microsoft Entra ID / Graph](entra-api.md) | `login.microsoftonline.com/{tenant}` · `graph.microsoft.com` | MSAL public client (PKCE; interactive + silent) | DPAPI file `…\TimeTracker\auth\msal_cache.bin` | `internal/entra` |

## Cross-cutting conventions

- **Never log auth material.** Cookies, CSRF tokens, `Authorization` headers,
  access/refresh tokens and ID-token claims are never logged, not even at
  `Debug` — only truncated response-body snippets on error (CLAUDE.md §8, §12).
- **Secrets at rest are encrypted.** Personio cookies and the GitHub token live in
  the Windows Credential Manager; the Entra/MSAL cache is a DPAPI-encrypted file
  (CurrentUser scope, atomic writes). Nothing sensitive is written to
  `config.toml`.
- **Redirects are a signal, not a path.** The Personio HTTP client does not follow
  redirects; a `30x → /login` is how an expired session is detected. See
  [personio-api.md](personio-api.md#error--session-handling).
- **No unattended prompts.** Plugin-facing token requests are silent-only; an
  interactive browser is opened only from an explicit user action.

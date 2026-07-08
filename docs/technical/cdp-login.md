# Personio login via Chrome DevTools Protocol

Personio's UI-API is cookie-authenticated and there is no headless credential
grant, so Hashpoint captures a session by driving a **real Chrome instance** and
reading the cookies out after the user logs in. This is the only place the app
starts a browser via CDP. Source: `internal/personio/auth_cdp.go`.

## Launch

`Login(ctx, cfg)` builds a `chromedp` exec-allocator against the system Chrome
(chromedp auto-locates the executable) with these flags (`auth_cdp.go:65`):

- `headless` — from `cfg.Headless`, **default `false`** so the user sees the login
  window (headless is for tests).
- `disable-gpu`, `no-first-run`, `no-default-browser-check` — always set.

chromedp binds the CDP endpoint to an **ephemeral loopback port** (no fixed
`9222`); the port lives only for the duration of the login and is managed by
chromedp. The browser is launched fresh per `Login` call and closed immediately
after the cookies are captured.

> **Security note.** The DevTools endpoint grants full control of the browser
> profile for its lifetime. Keeping it on an ephemeral, loopback-only port (rather
> than a well-known fixed port) and tearing the browser down right after capture
> limits the exposure window — this was a finding in the internal security audit
> (`docs/security-audit-2026-06-10.md`, "Personio-CDP-Port"). Do not reintroduce a
> fixed remote-debugging port.

## Flow

1. Navigate to `https://{tenant}.personio.de/login/index` with `network.Enable`
   active so cookies can be read (`auth_cdp.go:61`, `:84`).
2. Poll the browser location every **500 ms** (`waitForAuthenticated`,
   `auth_cdp.go:129`). The login counts as complete when the host is a Personio
   domain **and** the path no longer starts with `/login` or contains `/auth`. A
   `.app.personio.com`/`.app.personio.de` subdomain is preferred as the resulting
   **`AppHost`** (used for all subsequent API calls); a short (~750 ms) settle
   delay lets post-redirect cookies land (`auth_cdp.go:160`).
3. Read cookies with `network.GetCookies` and keep only Personio-domain cookies
   (`auth_cdp.go:199`), mapping each to a `SessionCookie`
   (`Name, Value, Domain, Path, Expires, Secure, HTTPOnly, SameSite`).
4. Assemble `Session{ Tenant, AppHost, Cookies, CapturedAt }`. If no `XSRF-TOKEN`
   cookie is present the login is treated as failed.
5. Validate the fresh session with a `GET /api/v1/navigation/context` (redirect to
   `/login` or 401/403 ⇒ `ErrSessionExpired`), then resolve and store the employee
   id. See [personio-api.md](personio-api.md).

## Output

The captured `Session` is persisted by the caller in the Windows Credential
Manager (`TimeTracker.PersonioSession`). No passwords, MFA secrets or SSO
credentials are ever seen by the app — only the resulting cookies. Nothing from
the browser is logged in plaintext.

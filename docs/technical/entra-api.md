# Microsoft Entra ID / Graph (MSAL)

Optional sign-in to a single Entra tenant, used to mint delegated tokens for
Microsoft Graph and other Entra-protected APIs (primarily for plugins). Source:
`internal/entra/{manager,cache,cipher_windows}.go`, `internal/plugin/hostapi.go`.
The user/admin setup guide is [`../user/entra-id.md`](../user/entra-id.md).

## Client & authority

- **MSAL-go public client** (`apps/public`) — no client secret, PKCE.
- **Authority** `https://login.microsoftonline.com/{tenant_id}`
  (`manager.go:143`). **Single-tenant only**: `common`/`organizations`/`consumers`
  are rejected at config validation.
- **Redirect URI** `http://localhost` (any free loopback port) by default,
  overridable via `Options.RedirectURI` for firewalled setups (`manager.go:137`).

## Scopes

- **Default login scope** `https://graph.microsoft.com/User.Read`
  (`DefaultLoginScopes`, `manager.go:48`). Callers can request other resource
  scopes (e.g. `Sites.Read.All`, `Calendars.Read`) per acquisition — MSAL forbids
  mixing resources in one call.
- **Reserved scopes `openid` / `profile` / `offline_access` MUST NOT be passed by
  the caller** (`manager.go:38`). MSAL injects them itself via
  `AppendDefaultScopes` **and** strictly compares the echoed scope claim; since
  Entra never echoes `offline_access` (it materialises as the `refresh_token`),
  passing it makes the acquisition fail with *"declined scopes are present:
  offline_access"*. Regression-guarded by `manager_test.go`.

## Token acquisition

`AcquireToken(ctx, scopes, allowInteractive)` (`manager.go`):

- **Silent** first — `AcquireTokenSilent(WithSilentAccount(...))`, 30 s timeout,
  cache-only. On a cache miss / expired refresh token / consent drift it returns
  `ErrInteractiveRequired`.
- **Interactive** — `AcquireTokenInteractive(WithRedirectURI(...))`, 5 min timeout,
  used only when `allowInteractive` is true (an explicit user login). Promptless on
  Entra-joined devices via PRT-SSO.
- `Login`, `Logout` (`RemoveAccount` + cache clear) and `Status`
  (`client.Accounts` → `PreferredUsername`, `HomeAccountID`) round out the surface.

The app makes **no direct Graph calls**; it mints access tokens that plugins use to
call `graph.microsoft.com` (or custom resources) themselves.

## Token cache

- File `{CacheDir}/msal_cache.bin` — typically
  `%LOCALAPPDATA%\TimeTracker\auth\msal_cache.bin` (`manager.go:140`).
- **DPAPI-encrypted**, CurrentUser scope (`cipher_windows.go`) — bound to the exact
  Windows user on the exact machine; unreadable elsewhere.
- **Atomic writes** — temp file, fsync, rename (`cache.go`). `Replace` loads &
  decrypts into MSAL; `Export` encrypts & writes (empty export deletes the file);
  `Clear` (logout) deletes it unconditionally. A missing/corrupt cache is treated
  as empty (the user re-authenticates).

## Plugin access

`HostAPI.RequestEntraToken(ctx, scopes)` (`hostapi.go`) routes to
`AcquireToken(ctx, scopes, allowInteractive=false)` — **silent only**, so a plugin
can never trigger a browser popup mid-session. Any failure (feature dormant, signed
out, refresh expired, scopes need consent) is collapsed to
`sdk.ErrEntraNotAvailable`. The refresh token never leaves the DPAPI-encrypted
cache; the plugin only receives a short-lived access token. See
[`../plugins/api.md`](../plugins/api.md).

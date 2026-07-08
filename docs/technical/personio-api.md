# Personio UI-API

Hashpoint syncs tracked time to Personio through the same **internal UI-API** the
Personio web app uses (not the public REST API). Auth is cookie-based and captured
interactively via Chrome — see [Personio login via CDP](cdp-login.md). This page
documents the HTTP contract. Source: `internal/personio/{uiclient,session,sync,import,session_windows}.go`.

## Hosts

- **Login** happens against `https://{tenant}.personio.de/login/index` (the CDP
  flow). See [cdp-login.md](cdp-login.md).
- **All API calls** go to `https://{AppHost}`, where `AppHost` is the app-shell
  host discovered during login — typically `{tenant}.app.personio.com` (or
  `.app.personio.de`). It is captured from the browser's `window.location` after
  authentication and stored in the session (`Session.AppHost`), because the login
  subdomain and the API subdomain differ.

## Authentication

Cookie-based, replayed on every request:

- **Session cookies** captured at login are loaded into a `net/http/cookiejar`
  (`uiclient.go`) and sent with each request. They are persisted (JSON) in the
  Windows Credential Manager under `TimeTracker.PersonioSession`
  (`session_windows.go`). Sessions older than `MaxSessionAge = 24h` are purged on
  read (`session.go`).
- **CSRF header** `x-athena-xsrf-token` (`uiclient.go:269`). Its value is the
  **URL-decoded** value of the `XSRF-TOKEN` cookie (`session.go`, via
  `url.QueryUnescape`; fallback: any cookie whose name contains `xsrf`/`csrf`). If
  no XSRF cookie is present after login, the session is rejected.

### HTTP client

- **Timeout** 15 s default (`uiclient.go`).
- **Redirects are NOT followed**: `CheckRedirect` returns `http.ErrUseLastResponse`
  (`uiclient.go:274`). A `30x` whose `Location` points at `/login` (or contains
  `/auth`) is the primary expired-session signal.
- Common headers on every call (`uiclient.go` `do`): `Accept: application/json…`,
  `x-athena-xsrf-token` (when present), `Origin: https://{AppHost}`,
  `Referer: https://{AppHost}/`; `Content-Type: application/json` on the `PUT`.

## Endpoints

### 1. GET `/api/v1/navigation/context`

Fetches the signed-in **employee ID**. `uiclient.go:120`.

- No query params, no body.
- Response (subset): `data.user.id` (int64) → the employee ID; a `0`/missing id is
  an error.
- Used to resolve `Session.EmployeeID` after login and before a sync.

### 2. GET `/svc/attendance-bff/v1/timesheet/{employee_id}`

Reads the attendance timecards for a date range. `uiclient.go:184`.

- **Path param** `{employee_id}` — int64.
- **Query params** (all set): `start_date`, `end_date` (both `YYYY-MM-DD`, local
  calendar dates), `timezone=Europe/Berlin`, `source=OVERTIME_SERVICE`.
- **Response**: `{ "timecards": [ { day_id, date, state, is_off_day,
  periods: [ { id, start, end, type, comment, project_id } ] } ] }`.
  - `state` gates writability — `Timecard.Trackable()` accepts
    `trackable | open | rejected` (case-insensitive); `locked`/`non_trackable`
    days cannot be written.
  - Times are local-naive `YYYY-MM-DDTHH:MM:SS`.
- Used by `SyncRange`, `Preflight` (peek before overwrite) and `ImportDay`.

### 3. PUT `/svc/attendance-api/v1/days/{day_id}?autoFix=true&usedInTimesheet=true`

Creates or overwrites a day's periods. `uiclient.go:239`.

- **Path param** `{day_id}` — a UUID v4. If the day is new, Personio creates it
  under this id; otherwise it is the day's existing id (from endpoint 2).
- **Fixed query params** `autoFix=true`, `usedInTimesheet=true` (match the web UI).
- **Request body** (`SetDayPayload`, `uiclient.go:214`):

  ```json
  {
    "employee_id": 4242,
    "periods": [
      {
        "id": "<uuid-v4>",
        "start": "2026-05-08T08:00:00",
        "end": "2026-05-08T10:00:00",
        "period_type": "work",
        "project_id": 4711,
        "comment": "#projekta #frontend — Login fix",
        "auto_generated": false
      }
    ],
    "original_periods": [ /* mirror of periods */ ],
    "geolocation": null,
    "is_from_clock_out": false
  }
  ```

  - `period_type` is always `"work"`; `auto_generated` always `false`
    (`sync.go`). `project_id` is `int64` or `null`. `id` is freshly generated per
    period. `original_periods` mirrors `periods`.
- **Response**: any `2xx` is success; the body is not parsed.

## Body & time format

- **Time**: local-naive `YYYY-MM-DDTHH:MM:SS` — no offset, no `Z`. Written with
  `.Local().Format("2006-01-02T15:04:05")`, read with
  `time.ParseInLocation(...)` (`sync.go`, `import.go`). Personio interprets these
  against the `timezone` sent to endpoint 2.
- **Comment schema**: `#Parent #Sub — description`. On export the tag hierarchy is
  rendered as the `#Parent #Sub` prefix and the block description follows ` — `. On
  import the schema is parsed to resolve/create the tag; only the text after ` — `
  becomes the block description (`sync.go`, `import.go`). See the user-facing
  [Personio sync doc](../user/personio.md) for the resolution order.

## Error & session handling

`personio.ErrSessionExpired` (`session.go`) is returned on:

1. **401** or **403** on any call (`uiclient.go`).
2. A **3xx** whose `Location` starts with `/login` or contains `/auth`.
3. **Missing XSRF cookie** after login.

Callers treat `errors.Is(err, ErrSessionExpired)` as: purge the stored session and
send the user (or plugin) back through an interactive login — never silently retry
with the same cookies. Other typed cases: `ErrNoSession` (nothing stored /
`session.go`), age-expired sessions (purged on read), and non-writable days
(`locked`/`non_trackable`) surfaced per-day during sync.

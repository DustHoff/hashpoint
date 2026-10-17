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
  `Referer: https://{AppHost}/`; `Content-Type: application/json` on the GraphQL `POST`.

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

### 3. POST `/graphql?op=TM_TrackAttendancePeriodsOnDay_v2026071301`

Replaces all periods of one local calendar day. `track.go` (`TrackPeriodsOnDay`).

> **Attendance migration (Oct 2026):** Personio moved attendance writes to
> GraphQL. The former `PUT /svc/attendance-api/v1/days/{day_id}?autoFix=true&usedInTimesheet=true`
> now answers `501 Not Implemented` and is no longer used. Reads (endpoint 2)
> are unchanged.

- **Persisted query**: `query` carries the SHA-256 hash of a document registered
  on Personio's side, not GraphQL text. Operation name (with version suffix) and
  hash are constants in `track.go`, taken from a HAR capture of the web app. When
  Personio ships a new version, refresh both from a new capture. An unknown
  operation (`PERSISTED_QUERY_NOT_FOUND`) maps to `ErrOperationOutdated`.
- **Day addressing**: by `assumedAttributionDate`; no `day_id` needed.
- **Request body**:

  ```json
  {
    "query": "<sha256 of persisted document>",
    "operationName": "TM_TrackAttendancePeriodsOnDay_v2026071301",
    "variables": {
      "input": {
        "autoFix": false,
        "origin": "ATTENDANCE_ORIGIN_WEB",
        "assumedAttributionDate": {"year": 2026, "month": 5, "day": 8},
        "personId": {"id": "4242"},
        "attendancePeriods": [
          {
            "id": {"id": "<uuid-v4>"},
            "type": "ATTENDANCE_PERIOD_TYPE_WORK",
            "startLocalTime": {"date": {"year": 2026, "month": 5, "day": 8}, "time": {"hours": 8, "minutes": 0}},
            "endLocalTime": {"date": {"year": 2026, "month": 5, "day": 8}, "time": {"hours": 10, "minutes": 0}},
            "projectId": {"id": "4711"},
            "comment": "#projekta #frontend — Login fix",
            "personId": {"id": "4242"},
            "origin": "ATTENDANCE_ORIGIN_WEB",
            "legacyBreakMinutes": 0,
            "startGeolocation": null,
            "endGeolocation": null,
            "duration": {"seconds": "7200"}
          }
        ]
      }
    }
  }
  ```

  - `attendancePeriods` is the **complete** day — periods missing from the
    list are deleted (confirmed by HAR). Personio refuses to drop *every*
    period of an existing day (the web UI only allows editing the last one),
    so `reuseExistingPeriodIDs` (`sync.go`) assigns the ids of the day's
    existing work periods, in start order, to the outgoing periods; surplus
    existing ones are dropped, extra outgoing ones keep fresh UUIDs.
  - `type` is always `ATTENDANCE_PERIOD_TYPE_WORK` (`sync.go`); `id` is freshly
    generated per period. `projectId` is `null` or `{"id":"…"}` — the object form
    follows the API's id convention but is not yet confirmed by a capture with a
    project. `time.seconds` is omitted when zero; `duration.seconds` is an
    int64 encoded as string (as the web app does).
- **Response**: `data.trackAttendancePeriodsOnDay.{original,saved}.attendancePeriodsList`.
  GraphQL reports domain errors with HTTP `200` in `errors[]`; any entry there,
  or a missing `saved`, is a failed write.

## Body & time format

- **Time (read)**: local-naive `YYYY-MM-DDTHH:MM:SS` — no offset, no `Z`; parsed
  with `time.ParseInLocation(...)` (`sync.go`, `import.go`). Personio interprets
  these against the `timezone` sent to endpoint 2.
- **Time (write)**: structured local date + time of day (`startLocalTime` /
  `endLocalTime`), derived via `.Local()` in `track.go`.
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
`session.go`), age-expired sessions (purged on read), non-writable days
(`locked`/`non_trackable`) surfaced per-day during sync, and
`ErrOperationOutdated` (`track.go`) when Personio no longer knows the persisted
GraphQL operation — fixed by a code update, not by re-login.

# Capability: `oncall_documentation`

Plugins advertising `oncall_documentation` receive completed off-duty
("Rufbereitschaft") documentation forms and push them into a downstream
documentation/ticketing system.

## When the host invokes Submit

Hashpoint enqueues an on-call doc whenever **all** of the following are
true for a closed tag block:

1. The block has an `end_time` set.
2. Some part of `[start_time, end_time)` falls outside the configured
   working window (`config.WorkSchedule`):
   - the block's local-time interval intersects a non-working weekday,
     OR
   - the block's clock window crosses `[StartHour, EndHour)` on a
     working weekday.
3. The block's tag — or any ancestor in the tag hierarchy — is listed
   in `config.OnCall.TagIDs`.

The exact rule is implemented in `internal/plugin/oncall.Qualifies`
and re-evaluated by `Recheck` after every block mutation. The user
fills in the form on the Rufbereitschaft tab; clicking "Übertragen"
fans the payload out to every running plugin advertising
`oncall_documentation` in parallel.

## Payload

```go
type OnCallDocument struct {
    LocalID      string       // "hashpoint:oncall:<doc-id>"
    BlockID      int64
    StartTime    time.Time    // UTC
    EndTime      time.Time    // UTC
    TagName      string       // resolved tag display name
    Application  string       // user-entered (impacted system)
    IncidentType IncidentType // "planned_maintenance" | "service_disruption"
    Solution     string       // user-entered, free text
}
```

- `Application`, `IncidentType`, `Solution` come straight from the form.
- `TagName` is the resolved display name (`#oncall/billing`, etc.). The
  plugin should NOT depend on a particular hierarchy — the user may
  rename or restructure tags without telling the plugin.
- `LocalID` is stable per Hashpoint document. **Use it as the remote
  dedup key** so retries do not create duplicate tickets.

## Response

```go
type SubmissionResult struct {
    ExternalRef string // displayed in the inbox as a chip
    ExternalURL string // displayed as a clickable link (optional)
}
```

Both fields are optional but at least one is recommended — without
either, the user has no anchor to the remote ticket from the
Hashpoint inbox.

## Idempotency

`Submit` MUST be idempotent. The host fan-out + retry logic assumes a
re-issued `Submit` with the same `LocalID` either:

- returns the *existing* `SubmissionResult` for that ticket, or
- updates the existing ticket in place and returns its (unchanged)
  reference.

It MUST NOT create a duplicate.

Recommended pattern: when filing the remote ticket, set a custom field
or label `hashpoint-local-id=<LocalID>` and search by it before
creating a new one.

## Error semantics

- Wrap `sdk.ErrTransient` for retryable failures (HTTP 5xx, timeouts,
  rate limits). The host marks the per-plugin submission as `failed`;
  the user can click "Erneut versuchen" and the host re-dispatches
  only to plugins whose latest row is `failed` (already-`submitted`
  rows are skipped).
- Wrap `sdk.ErrNotConfigured` when required config is missing. The
  host surfaces the message in the settings UI; the user fixes the
  config; the plugin reloads on save.
- Any other error is treated as a non-retryable failure and surfaced
  verbatim in the inbox under "Letzter Fehler".

## Fan-out semantics

Multiple plugins implementing `oncall_documentation` run side-by-side
— Submit is dispatched to all of them in parallel. Each plugin's
result is recorded in its own `oncall_submissions` row; the doc's
rolled-up status is:

| Submissions state               | Doc status   |
|---------------------------------|--------------|
| No submissions yet              | `draft`      |
| Any submission `pending`        | `pending`    |
| All submissions `submitted`     | `submitted`  |
| Mix of `submitted` + `failed`   | `partial`    |
| All submissions `failed`        | `failed`     |

Submitted rows are FINAL — retries skip them to avoid duplicate
tickets. To resubmit to a plugin that already succeeded, delete the
remote ticket and re-create the Hashpoint doc.

## Staleness

The user may re-tag or resize a block after a doc is enqueued. If the
block no longer `Qualifies` AND the doc is still in `draft` state,
Hashpoint sets `stale = true`. The inbox shows a yellow banner; the
user can dismiss the doc (deletes it) or keep it.

A `stale` doc that is moved back into qualification clears its stale
flag automatically.

## Deletion

Deleting the underlying tag block cascades the doc + submissions away
via FK. Hashpoint **does not** call the plugin to delete the remote
ticket — that would invite destructive surprises. The plugin author
is free to add a separate cleanup mechanism if they want.

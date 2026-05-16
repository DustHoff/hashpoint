# Capability: `tag_provider`

Plugins advertising `tag_provider` supply tags that Hashpoint imports
into its tag hierarchy. Typical use cases: project / activity lists
from Personio, ticket-projects from Jira, calendar categories from
Outlook. The host merges every imported path into the existing tag
tree without touching tags the user (or any prior import) already
manages — **the user-tag wins, always**.

The capability is paired with two HostAPI methods that are useful for
any plugin, regardless of whether it imports tags:

- `HostAPI.ListTags(ctx)` — read the host's current tag set.
- `HostAPI.PublishTags(ctx, []ImportedTag)` — push tags into the host
  store. Restricted to `tag_provider` plugins.

## When the host invokes the handler

The host pulls a `tag_provider` plugin's catalogue at three moments:

1. **At plugin launch** (after `Configure` succeeds, before the plugin
   is marked `running` to the UI).
2. **On `Configure`** (i.e. whenever the user saves new settings; the
   host stops + restarts the plugin, which loops back to step 1).
3. **On user request** — the Plugins tab shows a "Tags neu laden"
   action for every `tag_provider` plugin in `running` state.

A plugin may also **push** outside those moments by calling
`HostAPI.PublishTags()` whenever its upstream changes — useful when a
plugin runs a long-lived watcher against a remote system. The host
treats pull and push identically: every entry goes through
`EnsureByPath` and only paths the host has never seen are created.

## Payload

```go
type ImportedTag struct {
    Path        string // slash-separated, e.g. "jira/PROJ-123"
    Description string // optional; honoured only on first create
    Color       string // optional hex (e.g. "#7c3aed"); same rule
}

type Order struct {
    ID          string // opaque per-plugin de-dupe key
    Name        string // shown in the Auftrag combobox AND stored on the tag
    Description string // optional helper text in the dropdown
}

type TagOrderMapping struct {
    TagPath   string // slash-separated path, segments without leading '#'
    OrderName string // value stored on tags.order_name; "" when unmapped
}

type TagProviderHandler interface {
    ListTags(ctx context.Context) ([]ImportedTag, error)
    ListOrders(ctx context.Context) ([]Order, error)
    NotifyTagOrders(ctx context.Context, mappings []TagOrderMapping) error
}
```

`Path` is normalised by the same rules `TagRepository.EnsureByPath`
uses:

1. Split on `/`.
2. For each segment: strip a leading `#`, drop non-alphanumeric
   characters, then re-prefix `#`.
3. Empty segments (after normalisation) are dropped.

So `"#Jira / PROJ-123"`, `"jira/proj-123"`, and `"jira/PROJ-123"` all
resolve to the canonical `#jira` → `#proj123` hierarchy. Case-
insensitive matching applies to existing rows as well — an existing
`#Jira` is reused for an imported `#JIRA`.

## Merge contract

For each `ImportedTag`:

- **Walk the path**, segment by segment.
- For each segment, look up an existing tag with the same canonical
  name under the running parent (no parent ≙ root). If it exists,
  reuse it — no fields are touched, regardless of who created it.
- If it does not exist, create it.
- `Description` and `Color` are only applied **to the leaf** and only
  when the leaf was created by this call. An existing leaf — whether
  the user created it or a previous import did — keeps its existing
  values.

Consequence: an import that re-runs against the same plugin is
idempotent (no churn, no duplicate creates). A user who renames or
recolours a tag will see their changes survive every subsequent
import. The plugin has no path to mutate user-managed tags.

## HostAPI.ListTags

```go
type HostTag struct {
    ID       int64
    Name     string
    ParentID int64 // 0 for root tags
    Color    string
}

// Available on every plugin's HostAPI.
list, err := host.ListTags(ctx)
```

`ListTags` returns the full host tag set as a flat slice with parent
IDs (caller builds the tree). The projection deliberately omits
Personio identifiers and the `sync_to_personio` flag — plugins do not
need them. Order follows the repo's `(parent_id, name)` ordering.

A `process_autotag` plugin can use `ListTags` to check whether a
specific tag already exists before suggesting an `EnsureByPath` that
would create it. A `tag_provider` plugin can use it to compute a diff
against its own catalogue before deciding what to publish.

## HostAPI.PublishTags

```go
created, err := host.PublishTags(ctx, []sdk.ImportedTag{
    {Path: "jira/PROJ-123", Description: "Customer A onboarding"},
})
```

Restricted to plugins that advertise `CapTagProvider`. Other plugins
see `sdk.ErrPublishTagsNotAllowed` (wrapping a message that names the
calling plugin). Returns the count of leaves the call actually
created — existing paths report 0.

`PublishTags` is the same code path as the host-driven pull at plugin
launch; calling it from inside `ListTags` would be redundant. Use it
when the plugin watches a remote source and wants to react to remote
changes without waiting for the next user-triggered refresh.

## Lifecycle

There is no per-plugin tracking — once a tag exists in the host
store, it is just a tag. Consequences:

- Tags the plugin previously published but no longer reports in
  `ListTags()` are NOT removed. The host leaves them alone. Cleanup
  is the user's responsibility (manual delete via the Tag-Manager
  tab).
- A stopped or uninstalled `tag_provider` plugin contributes nothing
  going forward, but its past contributions remain.
- A user-deleted tag (via the Tag-Manager) is re-created on the next
  import. There is no "tombstone" — by design; the plugin cannot
  reliably know the user wanted that path gone forever.

If you need stricter lifecycle semantics for your use case, model the
tag hierarchy so plugin-managed tags live under a clearly named root
(`#jira/...`, `#personio/...`) and instruct users to delete that
whole subtree if they want a clean slate.

## Order assignments (`NotifyTagOrders`)

`NotifyTagOrders` is the third leg of `tag_provider` and the only one
where the host pushes to the plugin instead of pulling. The host
invokes it **fire-and-forget** on every user-initiated tag mutation —
`CreateTag`, `UpdateTag`, `DeleteTag` via the Tag-Manager. The
argument is a snapshot of every tag in the host store with its
currently-assigned `OrderName`; tags without a mapping appear with
`OrderName == ""` so the plugin can diff against its previous snapshot
and detect:

- **new mappings** — entry present this round, absent or empty last
  round;
- **unmappings** — entry's `OrderName` went from non-empty to empty;
- **deletions** — entry present last round, absent this round.

There is no delta wire format. The full set is sent every time, sorted
by `TagPath` (ASCII) so the plugin's diff does not need a re-sort
pass.

### When the host invokes the handler

After a successful commit in any of these App-layer methods:

| Method | Trigger |
| --- | --- |
| `App.CreateTag` | User adds a tag in the Tag-Manager (including with an `order_name` pre-filled). |
| `App.UpdateTag` | User saves changes — `order_name` change, rename, recolour, anything. |
| `App.DeleteTag` | User deletes a tag (the mapping disappears with it). |

Plugin-driven tag operations — `HostAPI.PublishTags`, the launch-pull,
`RefreshPluginTags` — do **not** trigger `NotifyTagOrders`. Those code
paths cannot touch `order_name`, so a notification would just be
noise.

The notification fires whether or not the changed field was
`order_name`. The plugin's snapshot diff filters down to "did anything
I care about change?" — that responsibility deliberately lives in the
plugin so the host does not have to track per-plugin interest.

### Payload semantics

- `TagPath` is slash-separated and segment-normalised: each segment is
  the canonical tag name with the leading `#` stripped. This matches
  the format the plugin originally submitted via `ImportedTag.Path`.
- `OrderName` is exactly the string stored on `tags.order_name`. May be
  a `Name` previously returned by this plugin's `ListOrders`, may be
  freitext from another plugin's catalogue, may be arbitrary user
  freitext. The host does not partition the snapshot per plugin — every
  running `tag_provider` plugin gets every mapping.
- Tags whose normalised path is empty (degenerate) are dropped.
- Tags with a dangling parent FK emit the partial path the walker
  could resolve. This should not happen given the schema's
  `ON DELETE CASCADE`, but is defended.

### Lifecycle and reliability

- **Fire-and-forget.** The host spawns one goroutine per running
  `tag_provider` plugin, applies `HostDeps.SubmitTimeout` to each call,
  and never retries. A dropped snapshot (timeout, RPC error, plugin
  returned non-nil error) is logged at `Debug` and forgotten — the
  next user mutation rebuilds and re-sends the current state, so the
  plugin self-heals on the next change.
- **No coalescing.** Two rapid mutations produce two notifications; the
  plugin may see the second snapshot before finishing with the first.
  Plugins that hit a rate-limited upstream should debounce themselves.
- **No bootstrap.** The plugin does not receive an initial snapshot at
  launch — the first `NotifyTagOrders` arrives with the first user
  mutation after start-up. A plugin that needs to know the current
  state immediately should call `HostAPI.ListTags` and build the
  initial snapshot from there.
- **Order is preserved across siblings.** The host dispatches
  notifications in lexicographic plugin-name order so test assertions
  can be deterministic, but the goroutines run in parallel — do not
  assume a specific arrival order at the plugin side.

### Implementation notes for plugin authors

- The method MUST be implemented even if the plugin has no interest in
  user-side order changes. Return `nil` immediately to opt out cheaply.
- Returning a non-nil error is logged but never surfaced — it cannot
  trigger a host-side retry, a banner, or a state transition. Treat
  the return value as informational.
- The supplied snapshot is owned by the host but never mutated after
  the call returns. A plugin that needs to retain it past the call
  body must copy.
- A plugin can call `HostAPI.PublishTags` from inside `NotifyTagOrders`
  (e.g. to materialise a new path the user introduced), but doing so
  in the hot path is discouraged — the call holds a goroutine and
  contends with the next user mutation. Prefer an internal queue.

## Orders (`ListOrders`)

`ListOrders` is the second leg of `tag_provider`. Unlike tags, orders
are **never persisted by the host** — they are live-pulled every time
the user opens the Tags tab and rendered in the per-tag *Auftrag*
combobox. Only the chosen order's `Name` is written to `tags.order_name`
when the user picks one; the user can also type freitext that bypasses
the catalogue entirely. The host treats the picked value as opaque
text — `order_name` does not feed into Personio sync or auto-tagging
today.

Implementation notes for plugin authors:

- The method MUST be implemented even if the plugin has no order
  catalogue. Return `(nil, nil)` to stay quiet.
- The host applies a per-plugin timeout from `HostDeps.SubmitTimeout`
  to each call. A slow plugin must not stall the user's tab-open;
  exceeding the budget drops *this plugin's* contribution from the
  result rather than failing the whole query.
- Return value is **not** cached: every Tags tab open re-pulls. Keep
  the call cheap or back it with the plugin's own short-lived cache.
- `ID` is opaque to the host. Use whatever uniquely identifies the
  upstream entity inside your plugin (a JIRA key, a ClickUp doc id,
  …); the host only uses it to key the React option list.

## Conflict examples

| Scenario | What the host does |
| --- | --- |
| Plugin imports `proj-x`; no tag exists yet | Create `#proj-x` with the plugin's description / color. |
| Plugin imports `proj-x`; user-created `#proj-x` exists | Reuse the existing tag. Plugin's description / color are ignored. |
| Plugin A imports `shared`, Plugin B imports `shared` | First one creates, second one reuses. Both report `created=0` after the first round. |
| Plugin imports `jira/PROJ-1`; only `#jira` exists | Reuse `#jira`, create `#proj1` under it (plugin's metadata on the leaf only). |

## Errors

- `ListTags` returning an error is logged at `Warn`; the import for
  that round is skipped. Other capabilities of the same plugin keep
  running.
- A single bad path inside an import (e.g. a path that normalises to
  empty) is logged at `Warn` and skipped; remaining entries still
  import. The returned `created` count reflects only the successful
  entries.
- `PublishTags` returning `ErrPublishTagsNotAllowed` is a programming
  error in the manifest — the plugin author forgot to declare
  `tag_provider`. Surface it; do not retry.

## Manifest

```toml
name        = "personio-projects"
version     = "0.1.0"
description = "Imports Personio projects + activities as Hashpoint tags"
capabilities = ["tag_provider"]
```

No additional config schema is required — but a plugin will typically
declare credentials (Personio session, Jira API token, …) via the
standard manifest mechanism. Those secrets are persisted in the
`plugin_settings` table exactly as for any other capability.

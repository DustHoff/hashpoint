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

type TagProviderHandler interface {
    ListTags(ctx context.Context) ([]ImportedTag, error)
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

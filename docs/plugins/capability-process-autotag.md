# Capability: `process_autotag`

Plugins advertising `process_autotag` declare a set of executable
basenames and supply an auto-tag for each focus (or comm-window) event
on one of those processes. They sit **behind** the user's hand-
maintained rules table: a focus event that matches an enabled rule is
tagged by that rule, and the plugin is never consulted.

## When the host invokes the handler

Two methods, called at different points:

### `ProcessNames(ctx) ([]string, error)`

Called once at every `Configure()` (i.e. at plugin launch and again
whenever the user saves new settings for the plugin). The handler
returns the lower-cased executable basenames (`teams.exe`,
`code.exe`, …) it wants to be consulted about. The host caches the
result; an empty (or nil) slice puts the handler in a dormant state
— it will not be consulted on any event.

### `Resolve(ctx, info) (ProcessAutoTagResult, error)`

Called on the orchestrator's hot path **only** when:

1. The focused (or comm-track) window's process basename matches one
   of the plugin's declared `ProcessNames`, **and**
2. No enabled user rule already matches the focused window.

The handler may still opt out for a specific `(processName, windowTitle)`
pair by returning `Match: false` — useful when the plugin wants to
ignore certain window titles (e.g. a Teams notification popup) without
removing the process from `ProcessNames`.

The host applies a tight per-call timeout
(`HostDeps.AutoTagResolveTimeout`, default 500 ms) because Resolve
runs synchronously on the focus-change loop. Plugins SHOULD respond
quickly; a slow plugin is dropped for that event and the orchestrator
moves on as if no plugin had claimed the process.

## Payload

```go
type ProcessFocusInfo struct {
    ProcessName     string // lower-cased basename (e.g. "code.exe")
    WindowTitle     string // verbatim; may be empty
    IsCommunication bool   // true → event came from the comm rail
}
```

```go
type ProcessAutoTagResult struct {
    Match       bool   // false → skip this event
    TagName     string // slash-separated hierarchy path
    Description string // optional, attached to the resulting block
}
```

`TagName` is a slash-separated path like `coding` or
`productivity/coding`. The host resolves it against the `tags` table
case-insensitively, creating any missing intermediate nodes. The
normaliser strips a leading `#`, drops non-alphanumeric characters,
and re-adds the `#` prefix, so `"Productivity / Coding"` and
`"#productivity/#coding"` all resolve to the same canonical
`#productivity` → `#coding` hierarchy.

## Conflict with user rules

User rules win, always. The resolver is only consulted when
`rules.ListEnabled` produces no hit for the current focus. This keeps
plugins from surprising the user — every tag a user sees come from
either their own rule table or a plugin they have explicitly
installed and enabled.

The orchestrator treats plugin-driven and rule-driven auto-tags as
**different sources** for lifecycle equality: switching between two
plugins (or between a plugin and a rule covering the same process)
closes the old block and opens a fresh one at the granularity floor,
identical to a normal rule-to-rule transition.

## Communication rail

Plugins are consulted for both rails:

- **Focus rail** — exactly one event per focus change. The handler
  receives `IsCommunication = false`.
- **Communication rail** — fires whenever the tracker's set of
  visible comm-process windows changes (Teams meeting opens/closes,
  title change). The host iterates over the live sessions in
  deterministic order (sorted by process name, then title) and
  returns the first non-nil result; `IsCommunication = true`.

A plugin can use the `IsCommunication` flag to choose between
focus-time tags and meeting-time tags for the same process.

## Multiple plugins claiming the same process

When two plugins both declare interest in the same basename, the host
picks the plugin whose name sorts first (lexicographically) and asks
it first. Subsequent claimants are consulted only if the first
returned `Match: false` or errored. This makes outcomes reproducible
across runs even when the running-plugin set is otherwise unordered.

## Error semantics

- `sdk.ErrNotConfigured` — surfaced as a settings-UI banner, exactly
  like for other capabilities. The plugin reloads on save.
- Any other error is logged at `Debug` (the resolver runs at hot
  cadence — Warn/Info noise is unacceptable) and the host falls
  through to the next candidate.

The host never panics on a plugin failure; the worst case is a focus
event that produces no auto-tag, identical to the no-plugin baseline.

## Tag-name lifecycle

Tags created on the plugin's behalf live in the same `tags` table the
user manages by hand. They are visible in the tag tree, can be edited
or deleted via the standard tag UI, and participate in any
hierarchical feature (on-call ancestry, Personio sync mapping, …).

The host caches `(plugin, tag-path) → tag-id` mappings to avoid
repeating the hierarchy walk on every focus event. If the user
deletes a plugin-created tag, the next block insert on that ID will
fail with an FK violation; the cache entry survives until the
plugin is reloaded, at which point a fresh `EnsureByPath` repopulates
it. Workaround for an immediate fix: toggle the plugin off and on
again from the settings UI.

## Minimal author skeleton

```go
package main

import (
    "context"

    sdk "github.com/dusthoff/hashpoint/plugin/sdk"
)

type meeter struct{}

func (meeter) Init(context.Context, sdk.HostAPI) error      { return nil }
func (meeter) Configure(context.Context, sdk.PluginConfig) error { return nil }
func (meeter) Metadata(_ context.Context) (sdk.Metadata, error) {
    return sdk.Metadata{
        Name:         "meeter",
        Version:      "0.1.0",
        APIVersion:   sdk.HostAPIVersion,
        Capabilities: []sdk.Capability{sdk.CapProcessAutoTag},
    }, nil
}

func (meeter) ProcessNames(_ context.Context) ([]string, error) {
    return []string{"teams.exe", "zoom.exe"}, nil
}

func (meeter) Resolve(_ context.Context, info sdk.ProcessFocusInfo) (sdk.ProcessAutoTagResult, error) {
    return sdk.ProcessAutoTagResult{
        Match:       true,
        TagName:     "meetings",
        Description: info.WindowTitle,
    }, nil
}

func main() { sdk.Serve(meeter{}) }
```

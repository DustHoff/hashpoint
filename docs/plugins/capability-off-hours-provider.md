# Capability: `off_hours_provider`

Plugins advertising `off_hours_provider` supply additional intervals
that should count as **off-hours** for Hashpoint's on-call qualification
— or, in the reverse direction, intervals that should be treated as
working-hours even though `WorkScheduleConfig` would normally call them
off-hours.

Typical use cases: dynamic public holidays (Easter, Pentecost, regional
state holidays), bridge days, company-wide shutdowns. The reverse
direction ("this Saturday is actually a planned shift, don't open
on-call docs for it") is also supported.

## When the host invokes the handler

The host calls `OffHours` **pull-based** during on-call qualification —
that is, after every block mutation (`Close`, re-tag, resize,
manual-range-create) that goes through `internal/plugin/oncall.Recheck`.
The handler is NOT called on every Hashpoint startup or on every timeline
render — only when the host actually needs to re-evaluate a block.

The host caches the result per plugin in a year-bucket in memory.
Cache entries are invalidated when the plugin reloads, stops, or
crashes. There is **no persistence** of plugin-supplied off-hours —
a stopped provider plugin contributes nothing to the timeline.

## Payload

```go
type OffHoursRequest struct {
    From time.Time // UTC, half-open lower bound
    To   time.Time // UTC, half-open upper bound
}

type OffHoursKind string
const (
    OffHoursAdd    OffHoursKind = "add"    // mark range as off-hours
    OffHoursRemove OffHoursKind = "remove" // mark range as working-hours
)

type OffHoursInterval struct {
    Start, End time.Time    // UTC, half-open [Start, End)
    Kind       OffHoursKind // empty string ≙ "add"
    Reason     string       // UI tooltip ("Christmas Day", "Planned shift")
    Source     string       // optional plugin-internal key (e.g. "DE-NW")
}

type OffHoursProviderHandler interface {
    OffHours(ctx context.Context, req OffHoursRequest) ([]OffHoursInterval, error)
}
```

The handler MUST return intervals that intersect `[req.From, req.To)`.
Returning intervals fully outside that range is not an error — the host
clips them silently — but wastes wire bandwidth.

## Semantics: how the timeline is computed

For a tag block `[block.start, block.end)` the host computes the
effective off-hours timeline in three passes:

1. **Base** — off-hours derived from `WorkScheduleConfig`: every minute
   outside `[StartHour, EndHour)` on a working weekday, plus every
   minute on a non-working weekday.
2. **Plugin adds** — every `Kind == "add"` interval from every running
   `off_hours_provider` plugin is unioned into the timeline.
3. **Plugin removes** — every `Kind == "remove"` interval is then
   subtracted from the timeline.

**`remove` wins globally.** A `remove` interval can carve a working-hour
window out of a non-working weekday and out of another plugin's `add`
interval. Conflicts between two plugins (A adds, B removes) are resolved
in B's favour.

The block qualifies for an on-call doc iff the final timeline intersects
`[block.start, block.end)` AND the block's tag (or any ancestor) is in
`config.oncall.tag_ids`.

## Idempotency and freshness

`OffHours` SHOULD be side-effect-free and deterministic for a given
`(From, To)` window — the host treats consecutive calls as equivalent
and caches the first response. If a plugin's underlying data source
changes (e.g. the user picked a different state in the plugin's config),
the plugin should signal that by calling `HostAPI.Log("info", …)` *and*
relying on the host's reload-on-`Configure` to drop the cache. The host
does NOT poll for cache freshness.

## Error semantics

- `sdk.ErrNotConfigured` — surfaced as a settings-UI banner. The plugin
  reloads after the user saves config. Capability calls are skipped
  until then.
- Any other error from `OffHours` is logged at `Debug` and the plugin's
  contribution for that window is treated as empty. The host falls
  through to the remaining plugins — a failing provider never blocks
  qualification.

The host never panics on a plugin failure; the worst case is that a
block that *should* qualify (because of the failed plugin's data)
doesn't get a doc until the plugin recovers and the block is mutated
again.

## No backfill

Installing or enabling an `off_hours_provider` plugin does NOT trigger a
retroactive rescan of historical blocks. The plugin's off-hours only
take effect on blocks that go through `Recheck` from that point forward
(block close, re-tag, resize, range create). This is a deliberate
trade-off — the alternative would be an open-ended scan with surprising
side-effects on long-unused docs.

If the user wants on-call docs for historical blocks after installing a
holiday provider, the practical workaround is to re-tag a block briefly
(any change triggers `Recheck`).

## Minimal author skeleton

```go
package main

import (
    "context"
    "time"

    sdk "github.com/dusthoff/hashpoint/plugin/sdk"
)

type holidays struct{}

func (holidays) Init(context.Context, sdk.HostAPI) error           { return nil }
func (holidays) Configure(context.Context, sdk.PluginConfig) error { return nil }
func (holidays) Metadata(_ context.Context) (sdk.Metadata, error) {
    return sdk.Metadata{
        Name:         "holidays-de",
        Version:      "0.1.0",
        APIVersion:   sdk.HostAPIVersion,
        Capabilities: []sdk.Capability{sdk.CapOffHoursProvider},
    }, nil
}

func (holidays) OffHours(_ context.Context, req sdk.OffHoursRequest) ([]sdk.OffHoursInterval, error) {
    // Example: hard-coded Christmas Day across all years in the window.
    var out []sdk.OffHoursInterval
    for y := req.From.Year(); y <= req.To.Year(); y++ {
        start := time.Date(y, time.December, 25, 0, 0, 0, 0, time.UTC)
        end := start.Add(24 * time.Hour)
        if end.After(req.From) && start.Before(req.To) {
            out = append(out, sdk.OffHoursInterval{
                Start: start, End: end,
                Kind: sdk.OffHoursAdd,
                Reason: "Christmas Day",
            })
        }
    }
    return out, nil
}

func main() { sdk.Serve(holidays{}) }
```

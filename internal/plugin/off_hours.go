package plugin

import (
	"context"
	"time"

	"github.com/dusthoff/hashpoint/internal/plugin/oncall"
	"github.com/dusthoff/hashpoint/plugin/sdk"
)

// OffHours implements oncall.OffHoursSource by fanning the query out to
// every running plugin that advertises CapOffHoursProvider, translating
// SDK types into the oncall package's types at the boundary so the
// caller stays free of plugin/sdk imports.
//
// Per-plugin results are cached in memory in year-buckets (one entry
// per local calendar year), filled lazily on first read. Cache entries
// are dropped implicitly when a plugin reloads (the pluginInstance is
// replaced) or explicitly when state transitions out of StateRunning
// (Stop, watchExit, recordFailure, recordNeedsConfig, recordDisabled).
// There is no on-disk persistence — a stopped provider plugin
// contributes nothing.
//
// Plugin errors are logged at Debug and treated as an empty
// contribution from that plugin; a failing provider never blocks
// qualification of unrelated plugins.
func (h *Host) OffHours(ctx context.Context, from, to time.Time) ([]oncall.OffHoursInterval, error) {
	if !to.After(from) {
		return nil, nil
	}

	type target struct {
		name string
		inst *pluginInstance
	}
	h.mu.RLock()
	var targets []target
	for name, p := range h.plugins {
		if p.state == StateRunning && p.offHours != nil {
			targets = append(targets, target{name: name, inst: p})
		}
	}
	h.mu.RUnlock()
	if len(targets) == 0 {
		return nil, nil
	}

	years := localYearsBetween(from, to)
	var out []oncall.OffHoursInterval
	for _, t := range targets {
		for _, year := range years {
			intervals, err := h.offHoursForYear(ctx, t.name, t.inst, year)
			if err != nil {
				h.log.Debug("off_hours fetch failed",
					"plugin", t.name, "year", year, "err", err)
				continue
			}
			for _, iv := range intervals {
				s := iv.Start
				e := iv.End
				if from.After(s) {
					s = from
				}
				if to.Before(e) {
					e = to
				}
				if !e.After(s) {
					continue
				}
				out = append(out, oncall.OffHoursInterval{
					Start: s,
					End:   e,
					Kind:  toOnCallKind(iv.Kind),
				})
			}
		}
	}
	return out, nil
}

// offHoursForYear returns the plugin's response for a single local
// calendar year, consulting the cache first and falling back to a
// single RPC call. The cache mutex serialises concurrent callers: a
// second goroutine arriving before the RPC completes blocks and reads
// the freshly-cached entry on its first peek.
//
// The cache mutex lives on the pluginInstance — never on the host
// mutex — so a slow plugin RPC cannot block unrelated host operations.
func (h *Host) offHoursForYear(ctx context.Context, name string, inst *pluginInstance, year int) ([]sdk.OffHoursInterval, error) {
	inst.offHoursCacheMu.Lock()
	defer inst.offHoursCacheMu.Unlock()
	if cached, ok := inst.offHoursCache[year]; ok {
		return cached, nil
	}
	yearStart := time.Date(year, time.January, 1, 0, 0, 0, 0, time.Local).UTC()
	yearEnd := time.Date(year+1, time.January, 1, 0, 0, 0, 0, time.Local).UTC()
	req := sdk.OffHoursRequest{From: yearStart, To: yearEnd}
	out, err := inst.offHours.OffHours(ctx, req)
	if err != nil {
		return nil, err
	}
	if inst.offHoursCache == nil {
		inst.offHoursCache = map[int][]sdk.OffHoursInterval{}
	}
	inst.offHoursCache[year] = out
	// Silence the linter: `name` is part of the signature for symmetry
	// with the other host helpers and shows up in error logs at the
	// caller. We use it indirectly via the log statement upstream.
	_ = name
	return out, nil
}

// localYearsBetween returns every calendar year (local timezone) the
// half-open window [from, to) touches. A two-day Christmas window in
// late December returns [Y, Y+1] iff Dec 31 23:59:59 → Jan 1 00:00:01
// crosses the year boundary in local time.
func localYearsBetween(from, to time.Time) []int {
	fy := from.In(time.Local).Year()
	ty := to.In(time.Local).Year()
	if ty < fy {
		ty = fy
	}
	out := make([]int, 0, ty-fy+1)
	for y := fy; y <= ty; y++ {
		out = append(out, y)
	}
	return out
}

// toOnCallKind maps SDK kinds onto the oncall package's internal enum.
// Unknown / empty kinds default to Add so plugin authors can leave the
// field unset for the common case.
func toOnCallKind(k sdk.OffHoursKind) oncall.OffHoursKind {
	if k == sdk.OffHoursRemove {
		return oncall.OffHoursRemove
	}
	return oncall.OffHoursAdd
}

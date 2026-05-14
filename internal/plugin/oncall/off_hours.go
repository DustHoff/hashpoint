package oncall

import (
	"context"
	"time"
)

// OffHoursKind discriminates a plugin contribution to the effective
// off-hours timeline. Add expands the timeline; Remove subtracts from
// it and wins globally — including against the WorkScheduleConfig
// baseline and against another plugin's Add for the same range.
//
// The host translates sdk.OffHoursKind values into this enum at the
// aggregator boundary so oncall code stays free of the SDK import.
type OffHoursKind int

// OffHoursKind values.
const (
	// OffHoursAdd marks a range as off-hours. Equivalent to "this is
	// outside the user's working window".
	OffHoursAdd OffHoursKind = iota
	// OffHoursRemove marks a range as working-hours even though
	// WorkScheduleConfig or another plugin's Add would otherwise call it
	// off-hours. Applied after all Adds during timeline composition.
	OffHoursRemove
)

// OffHoursInterval is one [Start, End) range with its kind. Both
// timestamps are UTC; Start < End is required (zero-length entries are
// silently dropped during qualification).
type OffHoursInterval struct {
	Start time.Time
	End   time.Time
	Kind  OffHoursKind
}

// OffHoursSource is what Qualifies consults to learn about plugin-
// supplied off-hours ranges over a given window. Implementations live
// in the host package (internal/plugin); the aggregator translates SDK
// types into this package's types at the boundary so oncall stays free
// of plugin/sdk imports.
//
// A nil OffHoursSource is legal — Qualifies degrades silently to the
// WorkScheduleConfig-only baseline. Errors from OffHours are surfaced
// verbatim by Qualifies; the caller (Recheck) treats them as a failed
// recheck and leaves the existing doc state untouched.
type OffHoursSource interface {
	OffHours(ctx context.Context, from, to time.Time) ([]OffHoursInterval, error)
}

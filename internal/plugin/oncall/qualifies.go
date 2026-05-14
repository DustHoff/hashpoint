// Package oncall is the host-side glue between tag-block lifecycle events
// and the OnCallRepository: it decides which blocks deserve a Rufbereitschaft
// row (Qualifies) and reconciles existing rows when a block's tag or time
// span changes (Recheck).
//
// Splitting Qualifies and Recheck into a separate package keeps the
// orchestrator agnostic of the plugin system — orchestrator.go calls
// oncall.Recheck after every block-mutating operation it owns, without
// importing internal/plugin or internal/storage's plugin shim.
package oncall

import (
	"context"
	"errors"
	"time"

	"github.com/dusthoff/hashpoint/internal/config"
	"github.com/dusthoff/hashpoint/internal/storage"
)

// TagAncestry resolves a tag's ancestor chain. The on-call check matches
// not only the block's direct tag but any ancestor — so configuring
// "#oncall" once covers "#oncall/billing", "#oncall/auth", etc.
//
// Implementations live next to the tag repo. The interface lives here so
// Qualifies stays unit-testable without a DB.
type TagAncestry interface {
	// AncestorsOf returns tagID itself plus every parent walking up the
	// tree to root. Order is unspecified — callers treat it as a set.
	AncestorsOf(ctx context.Context, tagID int64) ([]int64, error)
}

// Qualifies returns true iff the block should have an on-call doc row.
// Three conditions, all required:
//
//  1. The block is closed (EndTime != nil). Open blocks may still grow
//     or shrink, so we wait for closure before committing to a doc.
//
//  2. The block's tag (or any ancestor) is in onCallTagIDs.
//
//  3. Some part of [StartTime, EndTime) falls into the effective
//     off-hours timeline. The baseline is WorkScheduleConfig (non-working
//     weekdays + the [StartHour, EndHour) clock window); the optional
//     OffHoursSource lets plugins expand the timeline (Add) or carve
//     working-hour ranges out of it (Remove). Remove wins globally —
//     including against the WorkScheduleConfig baseline.
//
// onCallTagIDs being empty short-circuits to false: the on-call feature
// is dormant when the user hasn't configured any on-call tags.
//
// The cheap predicates (closed, tag set, ancestor walk) run first so the
// plugin RPC inside src is only paid for blocks that actually match the
// on-call tag set.
func Qualifies(ctx context.Context, b storage.TagBlock, ws config.WorkScheduleConfig, onCallTagIDs []int64, tags TagAncestry, src OffHoursSource) (bool, error) {
	if b.EndTime == nil {
		return false, nil
	}
	if len(onCallTagIDs) == 0 {
		return false, nil
	}
	end := *b.EndTime
	if !end.After(b.StartTime) {
		return false, nil
	}
	if tags == nil {
		return false, errors.New("oncall: nil TagAncestry — cannot resolve tag chain")
	}
	ancestors, err := tags.AncestorsOf(ctx, b.TagID)
	if err != nil {
		return false, err
	}
	onCallSet := make(map[int64]struct{}, len(onCallTagIDs))
	for _, id := range onCallTagIDs {
		onCallSet[id] = struct{}{}
	}
	matched := false
	for _, id := range ancestors {
		if _, ok := onCallSet[id]; ok {
			matched = true
			break
		}
	}
	if !matched {
		return false, nil
	}

	return effectiveTimelineNonEmpty(ctx, b.StartTime, end, ws, src)
}

// effectiveTimelineNonEmpty composes the off-hours timeline for the
// block window and reports whether it has any minute left after the
// remove pass. Splitting it out keeps Qualifies linear and lets tests
// poke at the timeline computation in isolation.
func effectiveTimelineNonEmpty(ctx context.Context, start, end time.Time, ws config.WorkScheduleConfig, src OffHoursSource) (bool, error) {
	adds := wsOffHoursIntervals(start, end, ws)
	var removes []interval

	if src != nil {
		contribs, err := src.OffHours(ctx, start, end)
		if err != nil {
			return false, err
		}
		for _, iv := range contribs {
			s := clipMax(iv.Start, start)
			e := clipMin(iv.End, end)
			if !e.After(s) {
				continue
			}
			if iv.Kind == OffHoursRemove {
				removes = append(removes, interval{start: s, end: e})
			} else {
				adds = append(adds, interval{start: s, end: e})
			}
		}
	}

	timeline := mergeIntervals(adds)
	for _, sub := range removes {
		timeline = subtractInterval(timeline, sub)
	}
	return len(timeline) > 0, nil
}

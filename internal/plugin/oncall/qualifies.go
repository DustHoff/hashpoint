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
//  2. Some part of [StartTime, EndTime) falls outside the configured
//     working-hours window — either on a non-working weekday OR outside
//     [StartHour, EndHour) on a working weekday.
//
//  3. The block's tag (or any ancestor) is in onCallTagIDs.
//
// onCallTagIDs being empty short-circuits to false: the on-call feature
// is dormant when the user hasn't configured any on-call tags.
func Qualifies(ctx context.Context, b storage.TagBlock, ws config.WorkScheduleConfig, onCallTagIDs []int64, tags TagAncestry) (bool, error) {
	if b.EndTime == nil {
		return false, nil
	}
	if len(onCallTagIDs) == 0 {
		return false, nil
	}
	if !overlapsOffHours(b.StartTime, *b.EndTime, ws) {
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
	for _, id := range ancestors {
		if _, ok := onCallSet[id]; ok {
			return true, nil
		}
	}
	return false, nil
}

// overlapsOffHours scans the interval [start, end) one local-day at a time
// and returns true the moment any minute falls outside the working window
// (either weekday excluded or hour-of-day excluded). The scan is bounded
// by the block's duration; in practice on-call blocks span a few hours,
// so the loop runs at most twice (cross-midnight case).
//
// Both start and end are converted to time.Local for the weekday and
// hour-of-day comparison — WorkScheduleConfig is documented as
// local-timezone semantics in §164–183 of config.go.
func overlapsOffHours(start, end time.Time, ws config.WorkScheduleConfig) bool {
	if !end.After(start) {
		return false
	}
	local := start.In(time.Local)
	endLocal := end.In(time.Local)

	for {
		// Examine the current local-day chunk: [local, min(endLocal, nextMidnight)).
		dayStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.Local)
		nextMidnight := dayStart.Add(24 * time.Hour)
		chunkEnd := endLocal
		if nextMidnight.Before(chunkEnd) {
			chunkEnd = nextMidnight
		}

		if !ws.IsWorkDay(local) {
			// Whole chunk is on a non-working weekday — anything here is off-hours.
			return true
		}
		// On a working weekday, off-hours means the chunk has any minute
		// outside [StartHour, EndHour). Compute the chunk's clock window
		// in [chunkStartHour, chunkEndHour) and intersect.
		chunkStartHourFloat := float64(local.Hour()) + float64(local.Minute())/60 + float64(local.Second())/3600
		// chunkEnd may be exactly nextMidnight ⇒ treat as 24.0 not 0.0.
		chunkEndHourFloat := 24.0
		if !chunkEnd.Equal(nextMidnight) {
			chunkEndHourFloat = float64(chunkEnd.Hour()) + float64(chunkEnd.Minute())/60 + float64(chunkEnd.Second())/3600
		}
		if chunkStartHourFloat < float64(ws.StartHour) || chunkEndHourFloat > float64(ws.EndHour) {
			return true
		}

		// Advance to the next day chunk.
		if !chunkEnd.Before(endLocal) {
			return false
		}
		local = nextMidnight
	}
}

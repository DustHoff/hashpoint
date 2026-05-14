package oncall

import (
	"sort"
	"time"

	"github.com/dusthoff/hashpoint/internal/config"
)

// interval is a half-open [start, end) window in UTC. Used internally
// by the timeline computation; never crosses package boundaries.
type interval struct {
	start, end time.Time
}

// wsOffHoursIntervals returns the WorkScheduleConfig-derived off-hours
// slices that intersect [windowStart, windowEnd). The walk runs in
// local time (WorkScheduleConfig semantics are local-tz) but the
// emitted bounds are clipped UTC instants so the caller can merge them
// directly with plugin-supplied UTC intervals.
//
// For a working weekday with start=8, end=18 the function emits two
// intervals per day: [00:00, 08:00) and [18:00, next-midnight). For a
// non-working weekday it emits one interval covering the whole day.
// Both are clipped to [windowStart, windowEnd).
func wsOffHoursIntervals(windowStart, windowEnd time.Time, ws config.WorkScheduleConfig) []interval {
	if !windowEnd.After(windowStart) {
		return nil
	}
	localStart := windowStart.In(time.Local)
	localEnd := windowEnd.In(time.Local)

	var out []interval
	cursor := localStart
	for cursor.Before(localEnd) {
		dayStart := time.Date(cursor.Year(), cursor.Month(), cursor.Day(), 0, 0, 0, 0, time.Local)
		nextMidnight := dayStart.Add(24 * time.Hour)
		chunkEnd := localEnd
		if nextMidnight.Before(chunkEnd) {
			chunkEnd = nextMidnight
		}

		if !ws.IsWorkDay(cursor) {
			out = appendIfNonEmpty(out, cursor, chunkEnd)
			cursor = nextMidnight
			continue
		}

		// Working day: emit pre-StartHour and post-EndHour slices that
		// intersect [cursor, chunkEnd). dayStart + ws.EndHour=24h
		// normalises to nextMidnight, so an EndHour of 24 produces an
		// empty post-work slice.
		startWindow := dayStart.Add(time.Duration(ws.StartHour) * time.Hour)
		endWindow := dayStart.Add(time.Duration(ws.EndHour) * time.Hour)

		// Pre-work: [dayStart, startWindow) clipped to chunk.
		out = appendIfNonEmpty(out, clipMax(cursor, dayStart), clipMin(chunkEnd, startWindow))
		// Post-work: [endWindow, nextMidnight) clipped to chunk.
		out = appendIfNonEmpty(out, clipMax(cursor, endWindow), clipMin(chunkEnd, nextMidnight))

		cursor = nextMidnight
	}
	return out
}

// mergeIntervals returns a sorted, coalesced copy of in. Overlapping or
// touching ([a,b) + [b,c) ⇒ [a,c)) entries are merged. Zero-length
// inputs are silently dropped.
func mergeIntervals(in []interval) []interval {
	if len(in) == 0 {
		return nil
	}
	cp := make([]interval, 0, len(in))
	for _, iv := range in {
		if iv.end.After(iv.start) {
			cp = append(cp, iv)
		}
	}
	if len(cp) == 0 {
		return nil
	}
	sort.Slice(cp, func(i, j int) bool { return cp[i].start.Before(cp[j].start) })
	out := []interval{cp[0]}
	for _, iv := range cp[1:] {
		last := &out[len(out)-1]
		if !iv.start.After(last.end) {
			if iv.end.After(last.end) {
				last.end = iv.end
			}
			continue
		}
		out = append(out, iv)
	}
	return out
}

// subtractInterval returns set minus sub. Entries fully covered by sub
// disappear; entries straddling sub's edges are split.
func subtractInterval(set []interval, sub interval) []interval {
	if !sub.end.After(sub.start) {
		return set
	}
	out := make([]interval, 0, len(set))
	for _, iv := range set {
		// Disjoint: keep as-is.
		if !iv.end.After(sub.start) || !iv.start.Before(sub.end) {
			out = append(out, iv)
			continue
		}
		// Pre-fragment: [iv.start, sub.start).
		if iv.start.Before(sub.start) {
			out = append(out, interval{start: iv.start, end: sub.start})
		}
		// Post-fragment: [sub.end, iv.end).
		if iv.end.After(sub.end) {
			out = append(out, interval{start: sub.end, end: iv.end})
		}
	}
	return out
}

// clipMin returns the earlier of a, b.
func clipMin(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// clipMax returns the later of a, b.
func clipMax(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// appendIfNonEmpty appends [start, end) to out iff end > start. Helper
// used by wsOffHoursIntervals to keep the call sites compact.
func appendIfNonEmpty(out []interval, start, end time.Time) []interval {
	if !end.After(start) {
		return out
	}
	return append(out, interval{start: start, end: end})
}

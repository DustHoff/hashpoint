package oncall

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dusthoff/hashpoint/internal/config"
	"github.com/dusthoff/hashpoint/internal/storage"
)

// staticSource is a deterministic OffHoursSource that returns a fixed
// slice of intervals regardless of (from, to). Tests filter the
// intervals themselves; clipping to [from, to) is Qualifies' job.
type staticSource struct {
	intervals []OffHoursInterval
	err       error
	calls     int
}

func (s *staticSource) OffHours(_ context.Context, _, _ time.Time) ([]OffHoursInterval, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.intervals, nil
}

func TestQualifies_OffHoursSource(t *testing.T) {
	loc := time.Local
	mk := func(date string, startH, endH int) storage.TagBlock {
		t.Helper()
		base, err := time.ParseInLocation("2006-01-02", date, loc)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		start := base.Add(time.Duration(startH) * time.Hour)
		end := base.Add(time.Duration(endH) * time.Hour)
		return storage.TagBlock{TagID: 100, StartTime: start, EndTime: &end}
	}
	day := func(date string, h, m int) time.Time {
		t.Helper()
		base, err := time.ParseInLocation("2006-01-02", date, loc)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		return base.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute)
	}

	ws := config.WorkScheduleConfig{
		StartHour: 8, EndHour: 18,
		WorkDays: []string{"Mon", "Tue", "Wed", "Thu", "Fri"},
	}
	ancestry := fakeAncestry{parents: map[int64]int64{}}

	tests := []struct {
		name      string
		block     storage.TagBlock
		intervals []OffHoursInterval
		want      bool
	}{
		{
			// Wed 2026-12-23 is a normal working day, ws says 10-12 is
			// in-hours. Plugin marks the whole day as off-hours via Add.
			name:  "Wed working hours but plugin adds whole day → qualifies",
			block: mk("2026-12-23", 10, 12),
			intervals: []OffHoursInterval{
				{Start: day("2026-12-23", 0, 0), End: day("2026-12-24", 0, 0), Kind: OffHoursAdd},
			},
			want: true,
		},
		{
			// Saturday is ws-off-hours. Plugin removes the whole Saturday
			// → block 10-12 no longer qualifies.
			name:  "Sat off-hours but plugin removes whole day → not qualified",
			block: mk("2026-05-09", 10, 12),
			intervals: []OffHoursInterval{
				{Start: day("2026-05-09", 0, 0), End: day("2026-05-10", 0, 0), Kind: OffHoursRemove},
			},
			want: false,
		},
		{
			// Plugin A adds the day, Plugin B removes the same day —
			// modelled as two intervals in a single source. Remove wins.
			name:  "Add + Remove on same range → remove wins → not qualified",
			block: mk("2026-12-23", 10, 12),
			intervals: []OffHoursInterval{
				{Start: day("2026-12-23", 0, 0), End: day("2026-12-24", 0, 0), Kind: OffHoursAdd},
				{Start: day("2026-12-23", 0, 0), End: day("2026-12-24", 0, 0), Kind: OffHoursRemove},
			},
			want: false,
		},
		{
			// Empty Kind is treated as Add (Go zero value default).
			name:  "Empty Kind defaults to Add → qualifies",
			block: mk("2026-12-23", 10, 12),
			intervals: []OffHoursInterval{
				{Start: day("2026-12-23", 0, 0), End: day("2026-12-24", 0, 0)},
			},
			want: true,
		},
		{
			// Plugin remove that only covers part of Saturday — block
			// at 10-12 falls inside the carved-out working window.
			name:  "Sat with partial remove inside the block → not qualified",
			block: mk("2026-05-09", 10, 12),
			intervals: []OffHoursInterval{
				{Start: day("2026-05-09", 9, 0), End: day("2026-05-09", 13, 0), Kind: OffHoursRemove},
			},
			want: false,
		},
		{
			// Plugin remove that doesn't fully cover the block — some
			// minute remains off-hours → still qualifies.
			name:  "Sat with partial remove leaving minutes off-hours → qualifies",
			block: mk("2026-05-09", 10, 12),
			intervals: []OffHoursInterval{
				{Start: day("2026-05-09", 11, 0), End: day("2026-05-09", 11, 30), Kind: OffHoursRemove},
			},
			want: true,
		},
		{
			// Empty plugin response on a Wed working-hours block → falls
			// back to ws-only, which says no off-hours → not qualified.
			name:      "Wed working hours, plugin returns nothing → not qualified",
			block:     mk("2026-12-23", 10, 12),
			intervals: nil,
			want:      false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			src := &staticSource{intervals: tc.intervals}
			got, err := Qualifies(context.Background(), tc.block, ws, []int64{100}, ancestry, src)
			if err != nil {
				t.Fatalf("Qualifies: %v", err)
			}
			if got != tc.want {
				t.Fatalf("want %v got %v", tc.want, got)
			}
		})
	}
}

func TestQualifies_OffHoursSource_ErrorPropagates(t *testing.T) {
	loc := time.Local
	base := time.Date(2026, 12, 23, 10, 0, 0, 0, loc)
	end := base.Add(2 * time.Hour)
	block := storage.TagBlock{TagID: 100, StartTime: base, EndTime: &end}
	ws := config.WorkScheduleConfig{
		StartHour: 8, EndHour: 18,
		WorkDays: []string{"Mon", "Tue", "Wed", "Thu", "Fri"},
	}
	ancestry := fakeAncestry{parents: map[int64]int64{}}

	wantErr := errors.New("plugin exploded")
	src := &staticSource{err: wantErr}
	_, err := Qualifies(context.Background(), block, ws, []int64{100}, ancestry, src)
	if !errors.Is(err, wantErr) {
		t.Fatalf("want wrap of plugin error; got %v", err)
	}
}

func TestQualifies_OffHoursSource_SkippedForNonOnCallTag(t *testing.T) {
	// Plugin RPC must NOT fire when the block's tag is outside the
	// on-call set — that's the cheap-path optimisation. A spying source
	// asserts the contract.
	loc := time.Local
	base := time.Date(2026, 5, 9, 10, 0, 0, 0, loc) // Saturday
	end := base.Add(2 * time.Hour)
	block := storage.TagBlock{TagID: 999, StartTime: base, EndTime: &end}
	ws := config.WorkScheduleConfig{
		StartHour: 8, EndHour: 18,
		WorkDays: []string{"Mon", "Tue", "Wed", "Thu", "Fri"},
	}
	ancestry := fakeAncestry{parents: map[int64]int64{}}

	src := &staticSource{}
	_, err := Qualifies(context.Background(), block, ws, []int64{100}, ancestry, src)
	if err != nil {
		t.Fatalf("Qualifies: %v", err)
	}
	if src.calls != 0 {
		t.Fatalf("plugin RPC fired %d times for non-matching tag; want 0", src.calls)
	}
}

func TestMergeIntervals(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := func(h int) time.Time { return t0.Add(time.Duration(h) * time.Hour) }

	tests := []struct {
		name string
		in   []interval
		want []interval
	}{
		{
			name: "empty",
			in:   nil,
			want: nil,
		},
		{
			name: "disjoint",
			in:   []interval{{at(0), at(2)}, {at(5), at(7)}},
			want: []interval{{at(0), at(2)}, {at(5), at(7)}},
		},
		{
			name: "touching coalesce",
			in:   []interval{{at(0), at(2)}, {at(2), at(4)}},
			want: []interval{{at(0), at(4)}},
		},
		{
			name: "overlapping coalesce",
			in:   []interval{{at(0), at(3)}, {at(2), at(4)}},
			want: []interval{{at(0), at(4)}},
		},
		{
			name: "out-of-order input still merges",
			in:   []interval{{at(5), at(7)}, {at(0), at(2)}, {at(1), at(6)}},
			want: []interval{{at(0), at(7)}},
		},
		{
			name: "zero-length dropped",
			in:   []interval{{at(0), at(2)}, {at(3), at(3)}, {at(5), at(7)}},
			want: []interval{{at(0), at(2)}, {at(5), at(7)}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeIntervals(tc.in)
			if !intervalsEqual(got, tc.want) {
				t.Fatalf("want %v got %v", tc.want, got)
			}
		})
	}
}

func TestSubtractInterval(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := func(h int) time.Time { return t0.Add(time.Duration(h) * time.Hour) }

	tests := []struct {
		name string
		set  []interval
		sub  interval
		want []interval
	}{
		{
			name: "disjoint left",
			set:  []interval{{at(5), at(7)}},
			sub:  interval{at(0), at(3)},
			want: []interval{{at(5), at(7)}},
		},
		{
			name: "disjoint right",
			set:  []interval{{at(0), at(3)}},
			sub:  interval{at(5), at(7)},
			want: []interval{{at(0), at(3)}},
		},
		{
			name: "complete coverage",
			set:  []interval{{at(2), at(4)}},
			sub:  interval{at(0), at(10)},
			want: []interval{},
		},
		{
			name: "carve middle",
			set:  []interval{{at(0), at(10)}},
			sub:  interval{at(4), at(6)},
			want: []interval{{at(0), at(4)}, {at(6), at(10)}},
		},
		{
			name: "trim left edge",
			set:  []interval{{at(0), at(10)}},
			sub:  interval{at(0), at(4)},
			want: []interval{{at(4), at(10)}},
		},
		{
			name: "trim right edge",
			set:  []interval{{at(0), at(10)}},
			sub:  interval{at(6), at(10)},
			want: []interval{{at(0), at(6)}},
		},
		{
			name: "zero-length sub is no-op",
			set:  []interval{{at(0), at(10)}},
			sub:  interval{at(5), at(5)},
			want: []interval{{at(0), at(10)}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := subtractInterval(tc.set, tc.sub)
			if !intervalsEqual(got, tc.want) {
				t.Fatalf("want %v got %v", tc.want, got)
			}
		})
	}
}

func intervalsEqual(a, b []interval) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !a[i].start.Equal(b[i].start) || !a[i].end.Equal(b[i].end) {
			return false
		}
	}
	return true
}

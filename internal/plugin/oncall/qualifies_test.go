package oncall

import (
	"context"
	"testing"
	"time"

	"github.com/onesi/hashpoint/internal/config"
	"github.com/onesi/hashpoint/internal/storage"
)

// fakeAncestry returns each tag as its own ancestor plus an explicit
// parent map (childID → parentID). Sufficient for the qualification
// tests; the real impl uses the TagRepo.
type fakeAncestry struct {
	parents map[int64]int64
}

func (f fakeAncestry) AncestorsOf(_ context.Context, id int64) ([]int64, error) {
	out := []int64{id}
	for {
		p, ok := f.parents[id]
		if !ok {
			return out, nil
		}
		out = append(out, p)
		id = p
	}
}

func TestQualifies(t *testing.T) {
	// 2026-05-04 is a Monday. Working window: 08–18 local, Mon–Fri.
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
	ws := config.WorkScheduleConfig{
		StartHour: 8, EndHour: 18,
		WorkDays: []string{"Mon", "Tue", "Wed", "Thu", "Fri"},
	}
	ancestry := fakeAncestry{parents: map[int64]int64{
		101: 100, // 101's parent is 100; 100 is in onCallTagIDs.
	}}

	tests := []struct {
		name   string
		block  storage.TagBlock
		tagIDs []int64
		want   bool
	}{
		{
			name:   "Mon evening after-hours, tagged oncall → qualifies",
			block:  mk("2026-05-04", 19, 21),
			tagIDs: []int64{100},
			want:   true,
		},
		{
			name:   "Mon middle-of-day, tagged oncall → not qualified (in working hours)",
			block:  mk("2026-05-04", 10, 12),
			tagIDs: []int64{100},
			want:   false,
		},
		{
			name:   "Saturday daytime, tagged oncall → qualifies (non-work weekday)",
			block:  mk("2026-05-09", 10, 12),
			tagIDs: []int64{100},
			want:   true,
		},
		{
			name:   "Mon evening, tag 999 (not in oncall set) → not qualified",
			block:  mk("2026-05-04", 19, 21),
			tagIDs: []int64{888},
			want:   false,
		},
		{
			name: "Mon evening, child tag whose ancestor is oncall → qualifies",
			block: storage.TagBlock{
				TagID:     101,
				StartTime: time.Date(2026, 5, 4, 19, 0, 0, 0, loc),
				EndTime:   ptrTime(time.Date(2026, 5, 4, 21, 0, 0, 0, loc)),
			},
			tagIDs: []int64{100},
			want:   true,
		},
		{
			name:   "Empty oncall set → never qualifies",
			block:  mk("2026-05-09", 10, 12),
			tagIDs: nil,
			want:   false,
		},
		{
			name: "Open block (no EndTime) → never qualifies",
			block: storage.TagBlock{
				TagID:     100,
				StartTime: time.Date(2026, 5, 4, 19, 0, 0, 0, loc),
				EndTime:   nil,
			},
			tagIDs: []int64{100},
			want:   false,
		},
		{
			name:   "Block crossing midnight Fri→Sat → qualifies (Sat is off-day)",
			block:  mk("2026-05-08", 22, 26), // 22:00 Fri – 02:00 Sat
			tagIDs: []int64{100},
			want:   true,
		},
		{
			name:   "Block ending exactly at 18:00 on Monday → not qualified",
			block:  mk("2026-05-04", 8, 18),
			tagIDs: []int64{100},
			want:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Qualifies(context.Background(), tc.block, ws, tc.tagIDs, ancestry)
			if err != nil {
				t.Fatalf("Qualifies: %v", err)
			}
			if got != tc.want {
				t.Fatalf("want %v got %v", tc.want, got)
			}
		})
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

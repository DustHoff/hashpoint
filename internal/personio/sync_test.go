package personio

import (
	"strings"
	"testing"
	"time"

	"github.com/onesi/hashpoint/internal/storage"
)

func ptr[T any](v T) *T { return &v }

func mustEnd(t *testing.T, s string) *time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return &v
}

func TestBuildPeriods_AggregatesAndDedups(t *testing.T) {
	t.Parallel()

	parent := storage.Tag{
		ID:                 1,
		Name:               "#projekta",
		PersonioProjectID:  ptr("PRJ"),
		PersonioActivityID: ptr("ACT"),
		SyncToPersonio:     true,
	}
	subA := storage.Tag{
		ID:          2,
		ParentID:    ptr(int64(1)),
		Name:        "#frontend",
		Description: ptr("Login"),
		SyncToPersonio: true,
	}
	subB := storage.Tag{
		ID:          3,
		ParentID:    ptr(int64(1)),
		Name:        "#meeting",
		SyncToPersonio: true,
	}
	tags := map[int64]storage.Tag{1: parent, 2: subA, 3: subB}

	day := time.Date(2026, 4, 25, 0, 0, 0, 0, time.UTC)
	blocks := []storage.FocusBlock{
		// First chunk on subA.
		{
			ID: 1, ProcessName: "code", WindowTitle: "x",
			StartTime: day.Add(9 * time.Hour),
			EndTime:   mustEnd(t, "2026-04-25T09:30:00Z"),
			TagID:     ptr(int64(2)),
		},
		// Second chunk on subA — duplicate comment, must dedupe.
		{
			ID: 2, ProcessName: "code", WindowTitle: "y",
			StartTime: day.Add(10 * time.Hour),
			EndTime:   mustEnd(t, "2026-04-25T10:30:00Z"),
			TagID:     ptr(int64(2)),
		},
		// subB — same parent mapping, different sub.
		{
			ID: 3, ProcessName: "teams", WindowTitle: "Standup",
			StartTime: day.Add(11 * time.Hour),
			EndTime:   mustEnd(t, "2026-04-25T11:30:00Z"),
			TagID:     ptr(int64(3)),
		},
		// Idle block — must be skipped.
		{
			ID: 4, ProcessName: "x", WindowTitle: "y",
			StartTime: day.Add(12 * time.Hour),
			EndTime:   mustEnd(t, "2026-04-25T12:30:00Z"),
			IsIdle:    true,
			TagID:     ptr(int64(2)),
		},
		// Untagged — must be skipped.
		{
			ID: 5, ProcessName: "x", WindowTitle: "y",
			StartTime: day.Add(13 * time.Hour),
			EndTime:   mustEnd(t, "2026-04-25T13:30:00Z"),
		},
	}

	periods := buildPeriods(blocks, tags)
	if len(periods) != 1 {
		t.Fatalf("expected 1 aggregated period, got %d", len(periods))
	}
	p := periods[0]
	if p.ProjectID != "PRJ" || p.ActivityID != "ACT" {
		t.Errorf("wrong mapping: %+v", p)
	}
	if len(p.BlockIDs) != 3 {
		t.Errorf("expected 3 blocks aggregated, got %d", len(p.BlockIDs))
	}
	if len(p.Comments) != 2 {
		t.Errorf("expected 2 deduped comments, got %d (%v)", len(p.Comments), p.Comments)
	}
	joined := strings.Join(p.Comments, "; ")
	if !strings.Contains(joined, "#projekta #frontend Login") {
		t.Errorf("missing frontend comment in %q", joined)
	}
	if !strings.Contains(joined, "#projekta #meeting") {
		t.Errorf("missing meeting comment in %q", joined)
	}
	expectedStart := day.Add(9 * time.Hour)
	if !p.Start.Equal(expectedStart) {
		t.Errorf("Start = %v want %v", p.Start, expectedStart)
	}
}

func TestShouldSkip(t *testing.T) {
	t.Parallel()
	parent := storage.Tag{ID: 1, Name: "#x", PersonioProjectID: ptr("PRJ"), SyncToPersonio: true}
	tags := map[int64]storage.Tag{1: parent}

	cases := []struct {
		name string
		b    storage.FocusBlock
		want bool
	}{
		{"open block", storage.FocusBlock{TagID: ptr(int64(1))}, true},
		{"untagged", storage.FocusBlock{EndTime: mustEnd(t, "2026-04-25T12:00:00Z")}, true},
		{"idle", storage.FocusBlock{IsIdle: true, TagID: ptr(int64(1)), EndTime: mustEnd(t, "2026-04-25T12:00:00Z")}, true},
		{"valid", storage.FocusBlock{TagID: ptr(int64(1)), EndTime: mustEnd(t, "2026-04-25T12:00:00Z")}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldSkip(tc.b, tags); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

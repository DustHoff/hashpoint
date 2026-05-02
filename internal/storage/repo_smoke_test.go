package storage

import (
	"context"
	"testing"
	"time"
)

func TestRecentlyUsedTagIDs(t *testing.T) {
	ctx := context.Background()
	db, err := OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	tagsRepo := NewTagRepo(db)
	tagA := &Tag{Name: "#a", SyncToPersonio: true}
	tagB := &Tag{Name: "#b", SyncToPersonio: true}
	tagC := &Tag{Name: "#c", SyncToPersonio: true}
	for _, tg := range []*Tag{tagA, tagB, tagC} {
		if err := tagsRepo.Create(ctx, tg); err != nil {
			t.Fatalf("create tag: %v", err)
		}
	}
	repo := NewTagBlockRepo(db)
	// Three blocks: A oldest, B middle, C newest. C must come first.
	mk := func(tagID int64, start time.Time) {
		end := start.Add(15 * time.Minute)
		b := &TagBlock{TagID: tagID, StartTime: start, EndTime: &end, IsManual: true}
		if err := repo.Open(ctx, b); err != nil {
			t.Fatalf("open block: %v", err)
		}
	}
	now := time.Now().UTC().Truncate(time.Second)
	mk(tagA.ID, now.Add(-72*time.Hour))
	mk(tagB.ID, now.Add(-48*time.Hour))
	mk(tagC.ID, now.Add(-24*time.Hour))
	mk(tagA.ID, now.Add(-2*time.Hour)) // A bumped to newest

	since := now.Add(-30 * 24 * time.Hour)
	got, err := repo.RecentlyUsedTagIDs(ctx, since, 10)
	if err != nil {
		t.Fatalf("RecentlyUsedTagIDs: %v", err)
	}
	want := []int64{tagA.ID, tagC.ID, tagB.ID}
	if len(got) != len(want) {
		t.Fatalf("len: want %d got %d (%v)", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("idx %d: want %d got %d", i, want[i], got[i])
		}
	}

	// Cutoff excludes ancient blocks.
	got, err = repo.RecentlyUsedTagIDs(ctx, now.Add(-36*time.Hour), 10)
	if err != nil {
		t.Fatalf("RecentlyUsedTagIDs cutoff: %v", err)
	}
	if len(got) != 2 || got[0] != tagA.ID || got[1] != tagC.ID {
		t.Fatalf("cutoff: got %v want [A C]", got)
	}
}

func TestProcessTrackLastEnd(t *testing.T) {
	ctx := context.Background()
	db, err := OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	r := NewProcessTrackRepo(db)
	pt := &ProcessTrack{
		ProcessName: "x",
		WindowTitle: "y",
		StartTime:   time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC),
	}
	if err := r.Open(ctx, pt); err != nil {
		t.Fatalf("open: %v", err)
	}
	end := time.Date(2026, 4, 29, 10, 30, 0, 0, time.UTC)
	if err := r.Close(ctx, pt.ID, end); err != nil {
		t.Fatalf("close: %v", err)
	}
	last, err := r.LastEnd(ctx)
	if err != nil {
		t.Fatalf("last end: %v", err)
	}
	t.Logf("LastEnd=%v zero=%v", last, last.IsZero())
	if !last.Equal(end) {
		t.Fatalf("LastEnd: want %v got %v", end, last)
	}
}

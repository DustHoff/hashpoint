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

func TestTagBlockResize(t *testing.T) {
	ctx := context.Background()
	db, err := OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	tagsRepo := NewTagRepo(db)
	tag := &Tag{Name: "#x", SyncToPersonio: true}
	if err := tagsRepo.Create(ctx, tag); err != nil {
		t.Fatalf("create tag: %v", err)
	}
	repo := NewTagBlockRepo(db)

	day := time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC)
	mk := func(start, end time.Time, manual bool) *TagBlock {
		b := &TagBlock{TagID: tag.ID, StartTime: start, EndTime: &end, IsManual: manual}
		if err := repo.Open(ctx, b); err != nil {
			t.Fatalf("open block: %v", err)
		}
		return b
	}

	// Layout: [09:00-10:00 auto] [11:00-12:00 manual]
	auto := mk(day.Add(9*time.Hour), day.Add(10*time.Hour), false)
	mk(day.Add(11*time.Hour), day.Add(12*time.Hour), true)

	// 1) Extend the auto block to the right into free space; should also
	//    flip is_manual=true because promoteToManual=true.
	newStart := day.Add(9 * time.Hour)
	newEnd := day.Add(10*time.Hour + 30*time.Minute)
	if err := repo.Resize(ctx, auto.ID, newStart, newEnd, true); err != nil {
		t.Fatalf("resize free: %v", err)
	}
	got, err := repo.Get(ctx, auto.ID)
	if err != nil || got == nil {
		t.Fatalf("get: %v %v", got, err)
	}
	if !got.StartTime.Equal(newStart) || got.EndTime == nil || !got.EndTime.Equal(newEnd) {
		t.Fatalf("range not applied: %v..%v", got.StartTime, got.EndTime)
	}
	if got.DurationSec != int64(90*60) {
		t.Fatalf("duration: want 5400 got %d", got.DurationSec)
	}
	if !got.IsManual {
		t.Fatal("expected promotion to manual")
	}

	// 2) Resize that would collide with the second block (10:30→11:30
	//    overlaps the [11:00,12:00) neighbor) must fail.
	collide := day.Add(11*time.Hour + 30*time.Minute)
	if err := repo.Resize(ctx, auto.ID, newStart, collide, false); err == nil {
		t.Fatal("expected overlap error")
	}

	// 3) Resize an open block must be rejected.
	openBlk := &TagBlock{TagID: tag.ID, StartTime: day.Add(14 * time.Hour), IsManual: true}
	if err := repo.Open(ctx, openBlk); err != nil {
		t.Fatalf("open openBlk: %v", err)
	}
	if err := repo.Resize(ctx, openBlk.ID, day.Add(14*time.Hour), day.Add(15*time.Hour), false); err == nil {
		t.Fatal("expected error on resizing an open block")
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

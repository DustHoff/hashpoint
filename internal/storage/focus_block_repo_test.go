package storage

import (
	"context"
	"errors"
	"testing"
	"time"
)

func setupDB(t *testing.T) *FocusBlockRepo {
	t.Helper()
	ctx := context.Background()
	db, err := OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewFocusBlockRepo(db)
}

func TestFocusBlockRepo_OpenCloseLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := setupDB(t)

	start := time.Date(2026, 4, 25, 9, 0, 0, 0, time.UTC)
	b := &FocusBlock{
		ProcessName: "code.exe",
		WindowTitle: "main.go",
		StartTime:   start,
	}
	if err := repo.Open(ctx, b); err != nil {
		t.Fatalf("open: %v", err)
	}
	if b.ID == 0 {
		t.Fatal("expected ID to be populated")
	}

	got, err := repo.LastOpen(ctx)
	if err != nil {
		t.Fatalf("last open: %v", err)
	}
	if got == nil || got.ID != b.ID {
		t.Fatal("expected our block as last open")
	}

	end := start.Add(15 * time.Minute)
	if err := repo.Close(ctx, b.ID, end); err != nil {
		t.Fatalf("close: %v", err)
	}

	got2, err := repo.Get(ctx, b.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got2.EndTime == nil || !got2.EndTime.Equal(end) {
		t.Fatalf("end_time wrong: %+v", got2.EndTime)
	}
	if got2.DurationSec != int64(15*60) {
		t.Errorf("duration=%d want 900", got2.DurationSec)
	}

	open, _ := repo.LastOpen(ctx)
	if open != nil {
		t.Fatal("expected no open block after close")
	}
}

func TestFocusBlockRepo_Split(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := setupDB(t)

	start := time.Date(2026, 4, 25, 9, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	b := &FocusBlock{
		ProcessName: "code.exe",
		WindowTitle: "main.go",
		StartTime:   start,
	}
	if err := repo.Open(ctx, b); err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := repo.Close(ctx, b.ID, end); err != nil {
		t.Fatalf("close: %v", err)
	}

	mid := start.Add(20 * time.Minute)
	right, err := repo.Split(ctx, b.ID, mid)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if right.ID == b.ID {
		t.Fatal("expected a new ID for the right side")
	}
	if !right.StartTime.Equal(mid) {
		t.Errorf("right start=%v", right.StartTime)
	}
	if right.EndTime == nil || !right.EndTime.Equal(end) {
		t.Errorf("right end=%v", right.EndTime)
	}

	left, _ := repo.Get(ctx, b.ID)
	if left.EndTime == nil || !left.EndTime.Equal(mid) {
		t.Errorf("left end should equal mid; got %v", left.EndTime)
	}
}

func TestFocusBlockRepo_ListByDay(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := setupDB(t)

	day := time.Date(2026, 4, 25, 0, 0, 0, 0, time.UTC)
	other := day.Add(48 * time.Hour)

	// Each block is closed before the next is opened — opens are now refused
	// when they would overlap an existing block, including a prior still-open
	// block (treated as +infinity).
	starts := []time.Time{
		day.Add(8 * time.Hour),
		day.Add(10 * time.Hour),
		other,
	}
	for i, ts := range starts {
		b := &FocusBlock{ProcessName: "p", WindowTitle: "t", StartTime: ts}
		if err := repo.Open(ctx, b); err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		if err := repo.Close(ctx, b.ID, ts.Add(30*time.Minute)); err != nil {
			t.Fatalf("close %d: %v", i, err)
		}
	}

	got, err := repo.ListByDay(ctx, day)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 blocks on the day, got %d", len(got))
	}
}

func TestFocusBlockRepo_SetTagAndSync(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := NewFocusBlockRepo(db)
	tags := NewTagRepo(db)

	tag := &Tag{Name: "#projekta", SyncToPersonio: true}
	if err := tags.Create(ctx, tag); err != nil {
		t.Fatalf("create tag: %v", err)
	}

	b := &FocusBlock{ProcessName: "p", WindowTitle: "t", StartTime: time.Now().UTC()}
	if err := repo.Open(ctx, b); err != nil {
		t.Fatalf("open: %v", err)
	}

	if err := repo.SetTag(ctx, b.ID, &tag.ID, true); err != nil {
		t.Fatalf("set tag: %v", err)
	}
	got, _ := repo.Get(ctx, b.ID)
	if got.TagID == nil || *got.TagID != tag.ID || !got.AutoTagged {
		t.Fatalf("tag not set correctly: %+v", got)
	}

	now := time.Now().UTC()
	if err := repo.MarkSynced(ctx, b.ID, "P-1", now); err != nil {
		t.Fatalf("mark synced: %v", err)
	}
	got2, _ := repo.Get(ctx, b.ID)
	if got2.PersonioID == nil || *got2.PersonioID != "P-1" {
		t.Fatalf("personio id missing: %+v", got2)
	}
}

func TestFocusBlockRepo_RejectsOverlap(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := setupDB(t)

	base := time.Date(2026, 4, 25, 9, 0, 0, 0, time.UTC)
	first := &FocusBlock{ProcessName: "p", WindowTitle: "t", StartTime: base}
	if err := repo.Open(ctx, first); err != nil {
		t.Fatalf("open first: %v", err)
	}
	end := base.Add(time.Hour)
	if err := repo.Close(ctx, first.ID, end); err != nil {
		t.Fatalf("close first: %v", err)
	}

	// New block starts inside the closed first block — must be refused.
	overlapping := &FocusBlock{ProcessName: "p", WindowTitle: "t", StartTime: base.Add(30 * time.Minute)}
	if err := repo.Open(ctx, overlapping); !errors.Is(err, ErrOverlap) {
		t.Fatalf("expected ErrOverlap on overlapping open, got %v", err)
	}

	// Touching at the boundary (start == prior end) must be allowed —
	// intervals are half-open [start, end).
	adjacent := &FocusBlock{ProcessName: "p", WindowTitle: "t", StartTime: end}
	if err := repo.Open(ctx, adjacent); err != nil {
		t.Fatalf("adjacent open should succeed, got %v", err)
	}

	// Updating an existing block to overlap another must also fail.
	moved := *adjacent
	moved.StartTime = base.Add(15 * time.Minute)
	endAdj := base.Add(75 * time.Minute)
	moved.EndTime = &endAdj
	if err := repo.Update(ctx, &moved); !errors.Is(err, ErrOverlap) {
		t.Fatalf("expected ErrOverlap on overlapping update, got %v", err)
	}
}

func TestFocusBlockRepo_OpenWhileAnotherStillOpen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := setupDB(t)

	first := &FocusBlock{ProcessName: "p", WindowTitle: "t", StartTime: time.Date(2026, 4, 25, 9, 0, 0, 0, time.UTC)}
	if err := repo.Open(ctx, first); err != nil {
		t.Fatalf("open first: %v", err)
	}
	// Second open while first is still open: an open block has end=+inf,
	// so any later block starts inside it.
	second := &FocusBlock{ProcessName: "p", WindowTitle: "t", StartTime: first.StartTime.Add(time.Hour)}
	if err := repo.Open(ctx, second); !errors.Is(err, ErrOverlap) {
		t.Fatalf("expected ErrOverlap when opening over a still-open block, got %v", err)
	}
}

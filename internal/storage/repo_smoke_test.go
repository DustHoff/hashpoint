package storage

import (
	"context"
	"testing"
	"time"
)

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

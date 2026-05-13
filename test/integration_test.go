//go:build integration

package test

import (
	"context"
	"testing"
	"time"

	"github.com/dusthoff/hashpoint/internal/storage"
)

// TestEndToEnd_TrackAndTagLifecycle exercises the full storage layer with a
// real SQLite (in-memory) DB across both tables introduced by the
// process-track / tag-block split: a process track records the raw focus
// interval, a tag block is opened in parallel and references a tag, and
// both close cleanly with durations computed from start/end timestamps.
func TestEndToEnd_TrackAndTagLifecycle(t *testing.T) {
	ctx := context.Background()
	db, err := storage.OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	tracks := storage.NewProcessTrackRepo(db)
	blocks := storage.NewTagBlockRepo(db)
	tags := storage.NewTagRepo(db)

	parent := &storage.Tag{Name: "#projekta", SyncToPersonio: true}
	if err := tags.Create(ctx, parent); err != nil {
		t.Fatalf("create tag: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	p := &storage.ProcessTrack{
		ProcessName: "code.exe",
		WindowTitle: "main.go",
		StartTime:   now,
	}
	if err := tracks.Open(ctx, p); err != nil {
		t.Fatalf("open process track: %v", err)
	}

	b := &storage.TagBlock{
		TagID:     parent.ID,
		StartTime: now,
		IsManual:  true,
	}
	if err := blocks.Open(ctx, b); err != nil {
		t.Fatalf("open tag block: %v", err)
	}

	end := now.Add(15 * time.Minute)
	if err := tracks.Close(ctx, p.ID, end); err != nil {
		t.Fatalf("close track: %v", err)
	}
	if err := blocks.Close(ctx, b.ID, end); err != nil {
		t.Fatalf("close block: %v", err)
	}

	gotTrack, err := tracks.Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("get track: %v", err)
	}
	if gotTrack.DurationSec != int64(15*60) {
		t.Errorf("track duration=%d, want %d", gotTrack.DurationSec, 15*60)
	}

	gotBlock, err := blocks.Get(ctx, b.ID)
	if err != nil {
		t.Fatalf("get block: %v", err)
	}
	if gotBlock.TagID != parent.ID {
		t.Errorf("block tag=%d, want %d", gotBlock.TagID, parent.ID)
	}
	if gotBlock.DurationSec != int64(15*60) {
		t.Errorf("block duration=%d, want %d", gotBlock.DurationSec, 15*60)
	}
}

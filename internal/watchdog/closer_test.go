package watchdog_test

import (
	"context"
	"testing"
	"time"

	"github.com/dusthoff/hashpoint/internal/storage"
	"github.com/dusthoff/hashpoint/internal/watchdog"
)

func TestCloseOpenBlocks(t *testing.T) {
	ctx := context.Background()
	db, err := storage.OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	tags := storage.NewTagRepo(db)
	blocks := storage.NewTagBlockRepo(db)

	tag, err := tags.EnsureByPath(ctx, "#work")
	if err != nil {
		t.Fatalf("ensure tag: %v", err)
	}

	start := time.Date(2026, 5, 29, 8, 0, 0, 0, time.UTC)
	open := &storage.TagBlock{TagID: tag.ID, StartTime: start}
	if err := blocks.Open(ctx, open); err != nil {
		t.Fatalf("open tag-block: %v", err)
	}

	at := start.Add(90 * time.Minute)
	n, err := watchdog.CloseOpenBlocks(ctx, blocks, at)
	if err != nil {
		t.Fatalf("close open blocks: %v", err)
	}
	if n != 1 {
		t.Fatalf("closed = %d, want 1", n)
	}

	// The block is now closed exactly at `at`.
	got, err := blocks.Get(ctx, open.ID)
	if err != nil {
		t.Fatalf("get block: %v", err)
	}
	if got.EndTime == nil {
		t.Fatal("block still open after close")
	}
	if !got.EndTime.Equal(at) {
		t.Fatalf("end_time = %v, want %v", got.EndTime, at)
	}

	// Nothing remains open.
	stillOpen, err := blocks.ListOpen(ctx)
	if err != nil {
		t.Fatalf("list open: %v", err)
	}
	if len(stillOpen) != 0 {
		t.Fatalf("%d blocks still open, want 0", len(stillOpen))
	}

	// Idempotent: a second pass closes nothing (the collector's own recovery
	// on restart is a harmless no-op backstop).
	n, err = watchdog.CloseOpenBlocks(ctx, blocks, at)
	if err != nil {
		t.Fatalf("close open blocks (2nd pass): %v", err)
	}
	if n != 0 {
		t.Fatalf("closed = %d on 2nd pass, want 0", n)
	}
}

//go:build integration

package test

import (
	"context"
	"testing"
	"time"

	"github.com/dusthoff/hashpoint/internal/storage"
)

// TestEndToEnd_FocusBlockLifecycle exercises the full storage layer with a
// real SQLite (in-memory) DB.
func TestEndToEnd_FocusBlockLifecycle(t *testing.T) {
	ctx := context.Background()
	db, err := storage.OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	blocks := storage.NewFocusBlockRepo(db)
	tags := storage.NewTagRepo(db)

	parent := &storage.Tag{Name: "#projekta", SyncToPersonio: true}
	if err := tags.Create(ctx, parent); err != nil {
		t.Fatalf("create tag: %v", err)
	}

	now := time.Now().UTC()
	b := &storage.FocusBlock{
		ProcessName: "code.exe",
		WindowTitle: "main.go",
		StartTime:   now,
	}
	if err := blocks.Open(ctx, b); err != nil {
		t.Fatalf("open block: %v", err)
	}

	if err := blocks.SetTag(ctx, b.ID, &parent.ID, false); err != nil {
		t.Fatalf("set tag: %v", err)
	}

	end := now.Add(15 * time.Minute)
	if err := blocks.Close(ctx, b.ID, end); err != nil {
		t.Fatalf("close: %v", err)
	}

	got, err := blocks.Get(ctx, b.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.TagID == nil || *got.TagID != parent.ID {
		t.Fatalf("tag missing on block")
	}
	if got.DurationSec != int64(15*60) {
		t.Errorf("duration=%d", got.DurationSec)
	}
}

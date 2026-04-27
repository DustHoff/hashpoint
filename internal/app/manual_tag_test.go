package app

import (
	"context"
	"testing"
	"time"

	"github.com/onesi/hashpoint/internal/storage"
)

// newTestApp wires an App against an in-memory SQLite DB. The tracker is left
// nil — the manual-tag flow tolerates that and the App tests only care about
// the block lifecycle / state machine, not focus polling.
func newTestApp(t *testing.T) (*App, *storage.FocusBlockRepo, *storage.TagRepo) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	blocks := storage.NewFocusBlockRepo(db)
	tags := storage.NewTagRepo(db)

	a := New(Deps{Blocks: blocks, Tags: tags})
	a.Startup(ctx)
	return a, blocks, tags
}

func mustCreateTag(t *testing.T, repo *storage.TagRepo, ctx context.Context, name string) storage.Tag {
	t.Helper()
	tag := storage.Tag{Name: name}
	if err := repo.Create(ctx, &tag); err != nil {
		t.Fatalf("create tag %q: %v", name, err)
	}
	return tag
}

func TestStartManualTag_OpensPlaceholderBlockWithTag(t *testing.T) {
	t.Parallel()
	a, blocks, tags := newTestApp(t)
	tag := mustCreateTag(t, tags, a.ctx, "#focus")

	if err := a.StartManualTag(tag.ID); err != nil {
		t.Fatalf("StartManualTag: %v", err)
	}
	id, active := a.IsManualTagActive()
	if !active || id == 0 {
		t.Fatalf("expected active manual block, got id=%d active=%v", id, active)
	}

	got, err := blocks.Get(a.ctx, id)
	if err != nil {
		t.Fatalf("get block: %v", err)
	}
	if !got.IsPlaceholder {
		t.Errorf("manual block must be a placeholder, got false")
	}
	if got.TagID == nil || *got.TagID != tag.ID {
		t.Errorf("tag mismatch: got %v want %d", got.TagID, tag.ID)
	}
	if got.EndTime != nil {
		t.Errorf("manual block must remain open, got end %v", *got.EndTime)
	}
	if got.ProcessName != "" || got.WindowTitle != "" {
		t.Errorf("manual block must have empty process/title, got %q / %q",
			got.ProcessName, got.WindowTitle)
	}
}

func TestStartManualTag_SwitchingClosesPreviousBlock(t *testing.T) {
	t.Parallel()
	a, blocks, tags := newTestApp(t)
	tagA := mustCreateTag(t, tags, a.ctx, "#a")
	tagB := mustCreateTag(t, tags, a.ctx, "#b")

	if err := a.StartManualTag(tagA.ID); err != nil {
		t.Fatalf("start A: %v", err)
	}
	firstID, _ := a.IsManualTagActive()

	// Tiny delay so the second block's start time is strictly after the first
	// block's close time — keeps the "switch closes previous" assertion clean
	// even on systems with low clock resolution.
	time.Sleep(2 * time.Millisecond)

	if err := a.StartManualTag(tagB.ID); err != nil {
		t.Fatalf("start B: %v", err)
	}
	secondID, active := a.IsManualTagActive()
	if !active {
		t.Fatalf("expected active manual block after switch")
	}
	if secondID == firstID {
		t.Fatalf("switching tags must produce a new block id; got same id %d", firstID)
	}

	first, err := blocks.Get(a.ctx, firstID)
	if err != nil {
		t.Fatalf("get first: %v", err)
	}
	if first.EndTime == nil {
		t.Errorf("previous manual block must be closed on switch")
	}

	second, err := blocks.Get(a.ctx, secondID)
	if err != nil {
		t.Fatalf("get second: %v", err)
	}
	if second.TagID == nil || *second.TagID != tagB.ID {
		t.Errorf("new block must carry tag B; got %v", second.TagID)
	}
	if second.EndTime != nil {
		t.Errorf("new manual block must be open")
	}
}

func TestStopManualTag_ClosesActiveBlockAndIsIdempotent(t *testing.T) {
	t.Parallel()
	a, blocks, tags := newTestApp(t)
	tag := mustCreateTag(t, tags, a.ctx, "#focus")

	if err := a.StartManualTag(tag.ID); err != nil {
		t.Fatalf("start: %v", err)
	}
	id, _ := a.IsManualTagActive()

	if err := a.StopManualTag(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if _, active := a.IsManualTagActive(); active {
		t.Fatalf("manual tag must be inactive after stop")
	}

	got, err := blocks.Get(a.ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.EndTime == nil {
		t.Errorf("stopped manual block must have end time")
	}

	// Calling Stop again on no active block is a no-op (used by the tray
	// "Kein Tag" item — the user may click it more than once).
	if err := a.StopManualTag(); err != nil {
		t.Errorf("second StopManualTag must be a no-op, got %v", err)
	}
}

func TestStartManualTag_RejectsInvalidTagID(t *testing.T) {
	t.Parallel()
	a, _, _ := newTestApp(t)
	if err := a.StartManualTag(0); err == nil {
		t.Errorf("expected error for tag id 0")
	}
	if err := a.StartManualTag(-1); err == nil {
		t.Errorf("expected error for negative tag id")
	}
}

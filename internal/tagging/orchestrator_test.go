package tagging

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/onesi/hashpoint/internal/storage"
)

// orchTestEnv wires an Orchestrator on top of an in-memory SQLite DB
// and seeds a couple of tags + rules used across the scenarios.
type orchTestEnv struct {
	t       *testing.T
	ctx     context.Context
	tracks  *storage.ProcessTrackRepo
	blocks  *storage.TagBlockRepo
	tags    *storage.TagRepo
	rules   *storage.RuleRepo
	orch    *Orchestrator
	tagWeb  int64
	tagCode int64
	ruleWeb int64
	now     time.Time
}

func newOrchEnv(t *testing.T, granularity time.Duration) *orchTestEnv {
	t.Helper()
	ctx := context.Background()
	db, err := storage.OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	tracks := storage.NewProcessTrackRepo(db)
	blocks := storage.NewTagBlockRepo(db)
	tagRepo := storage.NewTagRepo(db)
	ruleRepo := storage.NewRuleRepo(db)

	web := storage.Tag{Name: "#web", SyncToPersonio: true}
	if err := tagRepo.Create(ctx, &web); err != nil {
		t.Fatalf("create web tag: %v", err)
	}
	code := storage.Tag{Name: "#code", SyncToPersonio: true}
	if err := tagRepo.Create(ctx, &code); err != nil {
		t.Fatalf("create code tag: %v", err)
	}
	rule := storage.Rule{
		MatchField: storage.MatchProcessName,
		MatchType:  storage.MatchContains,
		Pattern:    "browser",
		TagID:      web.ID,
		Priority:   10,
		Enabled:    true,
	}
	if err := ruleRepo.Create(ctx, &rule); err != nil {
		t.Fatalf("create rule: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	orch := NewOrchestrator(blocks, tracks, ruleRepo, logger)
	orch.SetGranularity(granularity)

	now := time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC)
	orch.SetClock(func() time.Time { return now })

	return &orchTestEnv{
		t: t, ctx: ctx,
		tracks: tracks, blocks: blocks, tags: tagRepo, rules: ruleRepo,
		orch:    orch,
		tagWeb:  web.ID, tagCode: code.ID,
		ruleWeb: rule.ID,
		now:     now,
	}
}

func (e *orchTestEnv) advance(d time.Duration) time.Time {
	e.now = e.now.Add(d)
	e.orch.SetClock(func() time.Time { return e.now })
	return e.now
}

func (e *orchTestEnv) listTagBlocks() []storage.TagBlock {
	e.t.Helper()
	bs, err := e.blocks.ListByDay(e.ctx, e.now)
	if err != nil {
		e.t.Fatalf("list tag blocks: %v", err)
	}
	return bs
}

// TestAutoTagOpensAndCloses: a matching process opens an auto-tag block
// snapped to the granularity floor; a non-matching process closes it.
func TestAutoTagOpensAndCloses(t *testing.T) {
	e := newOrchEnv(t, 5*time.Minute)
	at1 := time.Date(2026, 4, 29, 10, 3, 0, 0, time.UTC) // floors to 10:00
	e.orch.OnFocusChanged(e.ctx, "browser.exe", "Hashpoint Wiki", at1)

	at2 := time.Date(2026, 4, 29, 10, 8, 0, 0, time.UTC) // floors to 10:05
	e.orch.OnFocusChanged(e.ctx, "notepad.exe", "scratch", at2)

	bs := e.listTagBlocks()
	if len(bs) != 1 {
		t.Fatalf("expected 1 tag block, got %d", len(bs))
	}
	got := bs[0]
	if got.IsManual {
		t.Fatalf("expected auto-tag block, got manual")
	}
	if got.TagID != e.tagWeb {
		t.Fatalf("expected web tag id=%d, got %d", e.tagWeb, got.TagID)
	}
	want := time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC)
	if !got.StartTime.Equal(want) {
		t.Fatalf("start: want %v got %v", want, got.StartTime)
	}
	wantEnd := time.Date(2026, 4, 29, 10, 5, 0, 0, time.UTC)
	if got.EndTime == nil || !got.EndTime.Equal(wantEnd) {
		t.Fatalf("end: want %v got %v", wantEnd, got.EndTime)
	}
}

// TestAutoTagInheritsRuleDescription: a rule with a non-empty description
// copies it onto the auto-tag block. Manual blocks paused by the auto run
// keep their original description (verified by the interruption test).
func TestAutoTagInheritsRuleDescription(t *testing.T) {
	e := newOrchEnv(t, 5*time.Minute)

	desc := "Recherche"
	descRule := storage.Rule{
		MatchField:  storage.MatchProcessName,
		MatchType:   storage.MatchContains,
		Pattern:     "editor",
		TagID:       e.tagCode,
		Description: &desc,
		Priority:    20,
		Enabled:     true,
	}
	if err := e.rules.Create(e.ctx, &descRule); err != nil {
		t.Fatalf("create rule: %v", err)
	}

	at1 := time.Date(2026, 4, 29, 10, 3, 0, 0, time.UTC) // floors to 10:00
	e.orch.OnFocusChanged(e.ctx, "editor.exe", "main.go", at1)

	at2 := time.Date(2026, 4, 29, 10, 8, 0, 0, time.UTC) // floors to 10:05
	e.orch.OnFocusChanged(e.ctx, "shell.exe", "bash", at2)

	bs := e.listTagBlocks()
	if len(bs) != 1 {
		t.Fatalf("expected 1 block, got %d: %+v", len(bs), bs)
	}
	if bs[0].Description == nil || *bs[0].Description != desc {
		t.Fatalf("description: want %q, got %v", desc, bs[0].Description)
	}
}

// TestZeroLengthAutoTagSuppressed: a sub-granularity match produces no block.
func TestZeroLengthAutoTagSuppressed(t *testing.T) {
	e := newOrchEnv(t, 5*time.Minute)
	at1 := time.Date(2026, 4, 29, 10, 3, 0, 0, time.UTC) // floors to 10:00
	e.orch.OnFocusChanged(e.ctx, "browser.exe", "x", at1)

	at2 := time.Date(2026, 4, 29, 10, 4, 0, 0, time.UTC) // also floors to 10:00
	e.orch.OnFocusChanged(e.ctx, "notepad.exe", "y", at2)

	if bs := e.listTagBlocks(); len(bs) != 0 {
		t.Fatalf("expected zero-length suppression, got %d blocks: %+v", len(bs), bs)
	}
}

// TestManualOpenEndedAndAutoInterruption verifies the user's described flow:
// manual open-ended is interrupted by auto-tag and resumes after.
func TestManualOpenEndedAndAutoInterruption(t *testing.T) {
	e := newOrchEnv(t, 5*time.Minute)

	// 10:00 — no focus yet. User starts manual #code via tray.
	if err := e.orch.StartManualOpenEnded(e.ctx, e.tagCode, "ticket-42"); err != nil {
		t.Fatalf("start manual: %v", err)
	}

	// 10:07 — non-matching process focused.
	e.advance(7 * time.Minute)
	e.orch.OnFocusChanged(e.ctx, "notepad.exe", "scratch", e.now)

	// 10:13 — matching process. Manual should close at floor(10:13)=10:10,
	// auto-tag opens at 10:10.
	e.advance(6 * time.Minute)
	e.orch.OnFocusChanged(e.ctx, "browser.exe", "wiki", e.now)

	// 10:25 — non-matching again. Auto closes at 10:25, manual resumes.
	e.advance(12 * time.Minute)
	e.orch.OnFocusChanged(e.ctx, "notepad.exe", "scratch", e.now)

	// 10:35 — user explicitly stops the manual.
	e.advance(10 * time.Minute)
	if err := e.orch.StopManualOpenEnded(e.ctx); err != nil {
		t.Fatalf("stop manual: %v", err)
	}

	bs := e.listTagBlocks()
	if len(bs) != 3 {
		t.Fatalf("expected 3 blocks (manual, auto, manual-resumed), got %d: %+v", len(bs), bs)
	}
	// 1) Manual #code 10:00 - 10:10
	if !bs[0].IsManual || bs[0].TagID != e.tagCode {
		t.Errorf("block[0]: want manual #code, got %+v", bs[0])
	}
	if !bs[0].StartTime.Equal(time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("block[0].start: want 10:00, got %v", bs[0].StartTime)
	}
	if bs[0].EndTime == nil || !bs[0].EndTime.Equal(time.Date(2026, 4, 29, 10, 10, 0, 0, time.UTC)) {
		t.Errorf("block[0].end: want 10:10, got %v", bs[0].EndTime)
	}
	// 2) Auto #web 10:10 - 10:25
	if bs[1].IsManual || bs[1].TagID != e.tagWeb {
		t.Errorf("block[1]: want auto #web, got %+v", bs[1])
	}
	if !bs[1].StartTime.Equal(time.Date(2026, 4, 29, 10, 10, 0, 0, time.UTC)) {
		t.Errorf("block[1].start: want 10:10, got %v", bs[1].StartTime)
	}
	if bs[1].EndTime == nil || !bs[1].EndTime.Equal(time.Date(2026, 4, 29, 10, 25, 0, 0, time.UTC)) {
		t.Errorf("block[1].end: want 10:25, got %v", bs[1].EndTime)
	}
	// 3) Manual #code resumed 10:25 - 10:35 (with description preserved)
	if !bs[2].IsManual || bs[2].TagID != e.tagCode {
		t.Errorf("block[2]: want manual #code, got %+v", bs[2])
	}
	if bs[2].Description == nil || *bs[2].Description != "ticket-42" {
		t.Errorf("block[2] description: want ticket-42, got %v", bs[2].Description)
	}
	if !bs[2].StartTime.Equal(time.Date(2026, 4, 29, 10, 25, 0, 0, time.UTC)) {
		t.Errorf("block[2].start: want 10:25, got %v", bs[2].StartTime)
	}
	if bs[2].EndTime == nil || !bs[2].EndTime.Equal(time.Date(2026, 4, 29, 10, 35, 0, 0, time.UTC)) {
		t.Errorf("block[2].end: want 10:35, got %v", bs[2].EndTime)
	}
}

// TestStartManualWhileAutoActiveDefers: starting a manual while an auto-tag
// runs should NOT create the manual block now — it's deferred until the auto
// closes.
func TestStartManualWhileAutoActiveDefers(t *testing.T) {
	e := newOrchEnv(t, 5*time.Minute)

	// 10:00 — auto-tag opens via matching process.
	e.orch.OnFocusChanged(e.ctx, "browser.exe", "x", e.now)

	// 10:05 — user starts manual while auto runs.
	e.advance(5 * time.Minute)
	if err := e.orch.StartManualOpenEnded(e.ctx, e.tagCode, "deferred"); err != nil {
		t.Fatalf("start manual: %v", err)
	}

	// At this point only the auto block should exist (still open).
	bs := e.listTagBlocks()
	if len(bs) != 1 {
		t.Fatalf("expected 1 block (auto only), got %d: %+v", len(bs), bs)
	}
	if bs[0].IsManual {
		t.Fatalf("expected auto, got manual")
	}

	// 10:15 — non-matching process. Auto closes at 10:15, manual opens.
	e.advance(10 * time.Minute)
	e.orch.OnFocusChanged(e.ctx, "notepad.exe", "y", e.now)

	bs = e.listTagBlocks()
	if len(bs) != 2 {
		t.Fatalf("expected 2 blocks, got %d: %+v", len(bs), bs)
	}
	if !bs[1].IsManual || bs[1].TagID != e.tagCode {
		t.Errorf("block[1]: want manual #code, got %+v", bs[1])
	}
	if bs[1].EndTime != nil {
		t.Errorf("block[1] should be open, got end=%v", bs[1].EndTime)
	}
}

// TestManualRangeCarvesAutoBlocks verifies the user's described overlap
// behaviour: a manual range trims/splits/deletes auto blocks it covers.
func TestManualRangeCarvesAutoBlocks(t *testing.T) {
	e := newOrchEnv(t, 5*time.Minute)
	// Build an auto block 10:00 - 10:30 by simulating focus.
	e.orch.OnFocusChanged(e.ctx, "browser.exe", "x", e.now)
	e.advance(30 * time.Minute)
	e.orch.OnFocusChanged(e.ctx, "notepad.exe", "y", e.now)

	bs := e.listTagBlocks()
	if len(bs) != 1 || !bs[0].StartTime.Equal(time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("setup failed, blocks: %+v", bs)
	}

	// Carve a manual range 10:10 - 10:20 inside the auto block — should
	// produce: auto[10:00-10:10], manual[10:10-10:20], auto[10:20-10:30].
	from := time.Date(2026, 4, 29, 10, 10, 0, 0, time.UTC)
	to := time.Date(2026, 4, 29, 10, 20, 0, 0, time.UTC)
	if err := e.orch.CreateManualRange(e.ctx, e.tagCode, "carve", from, to); err != nil {
		t.Fatalf("create manual range: %v", err)
	}

	bs = e.listTagBlocks()
	if len(bs) != 3 {
		t.Fatalf("expected 3 blocks (split), got %d: %+v", len(bs), bs)
	}
	// Sorted by start.
	if bs[0].IsManual || bs[0].TagID != e.tagWeb {
		t.Errorf("left: want auto #web, got %+v", bs[0])
	}
	if !bs[1].IsManual || bs[1].TagID != e.tagCode {
		t.Errorf("middle: want manual #code, got %+v", bs[1])
	}
	if bs[2].IsManual || bs[2].TagID != e.tagWeb {
		t.Errorf("right: want auto #web, got %+v", bs[2])
	}
}

// TestManualRangeRejectedOnManualOverlap: manual-vs-manual overlap is an
// error per spec.
func TestManualRangeRejectedOnManualOverlap(t *testing.T) {
	e := newOrchEnv(t, 5*time.Minute)
	from1 := time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC)
	to1 := time.Date(2026, 4, 29, 10, 30, 0, 0, time.UTC)
	if err := e.orch.CreateManualRange(e.ctx, e.tagCode, "first", from1, to1); err != nil {
		t.Fatalf("first manual: %v", err)
	}
	from2 := time.Date(2026, 4, 29, 10, 15, 0, 0, time.UTC)
	to2 := time.Date(2026, 4, 29, 10, 45, 0, 0, time.UTC)
	err := e.orch.CreateManualRange(e.ctx, e.tagWeb, "second", from2, to2)
	if err == nil {
		t.Fatalf("expected overlap error, got nil")
	}
}

// TestStartupClosesDanglingManual: a manual block left open across a restart
// should be auto-closed at the last process-track end.
func TestStartupClosesDanglingManual(t *testing.T) {
	e := newOrchEnv(t, 5*time.Minute)

	// Open a manual block, then close the orchestrator state without stopping.
	if err := e.orch.StartManualOpenEnded(e.ctx, e.tagCode, "left-open"); err != nil {
		t.Fatalf("start manual: %v", err)
	}
	// Simulate a process track ending at 10:30.
	pt := &storage.ProcessTrack{
		ProcessName: "x",
		WindowTitle: "x",
		StartTime:   e.now,
	}
	if err := e.tracks.Open(e.ctx, pt); err != nil {
		t.Fatalf("open track: %v", err)
	}
	endAt := e.now.Add(30 * time.Minute)
	if err := e.tracks.Close(e.ctx, pt.ID, endAt); err != nil {
		t.Fatalf("close track: %v", err)
	}

	// Build a fresh orchestrator (simulating restart).
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	fresh := NewOrchestrator(e.blocks, e.tracks, e.rules, logger)
	fresh.SetGranularity(5 * time.Minute)
	fresh.SetClock(func() time.Time { return endAt.Add(time.Hour) })

	if err := fresh.CloseDanglingManualAtStartup(e.ctx, endAt.Add(time.Hour)); err != nil {
		t.Fatalf("close dangling: %v", err)
	}

	bs := e.listTagBlocks()
	if len(bs) != 1 {
		t.Fatalf("expected 1 closed manual block, got %d", len(bs))
	}
	if bs[0].EndTime == nil {
		t.Fatalf("manual still open after startup cleanup: %+v", bs[0])
	}
	want := time.Date(2026, 4, 29, 10, 30, 0, 0, time.UTC)
	if !bs[0].EndTime.Equal(want) {
		t.Errorf("end: want %v got %v", want, bs[0].EndTime)
	}
}

// TestResizeBlockSnapsAndPromotesAuto: an auto-tag block resized through
// the orchestrator snaps both edges to granularity, persists the new
// range, and is flipped to is_manual=true. A second resize that would
// overlap an existing neighbor is rejected and leaves the block intact.
func TestResizeBlockSnapsAndPromotesAuto(t *testing.T) {
	e := newOrchEnv(t, 15*time.Minute)

	// Seed: auto block 10:00-10:15 (driven by browser focus), then a
	// manual block 11:00-12:00 to act as the right-hand neighbor.
	e.orch.OnFocusChanged(e.ctx, "browser.exe", "Wiki", e.now)
	e.orch.OnFocusChanged(e.ctx, "notepad.exe", "x", e.advance(15*time.Minute))

	manualStart := time.Date(2026, 4, 29, 11, 0, 0, 0, time.UTC)
	manualEnd := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	if err := e.orch.CreateManualRange(e.ctx, e.tagCode, "", manualStart, manualEnd); err != nil {
		t.Fatalf("seed manual: %v", err)
	}

	bs := e.listTagBlocks()
	if len(bs) != 2 {
		t.Fatalf("expected 2 seed blocks, got %d", len(bs))
	}
	autoID := bs[0].ID
	if bs[0].IsManual {
		t.Fatal("expected first block to be auto")
	}

	// Resize the auto block out to 10:42 (should snap end up to 10:45)
	// and back the start to 09:55 (should snap floor to 09:45).
	newStart := time.Date(2026, 4, 29, 9, 55, 0, 0, time.UTC)
	newEnd := time.Date(2026, 4, 29, 10, 42, 0, 0, time.UTC)
	if err := e.orch.ResizeBlock(e.ctx, autoID, newStart, newEnd); err != nil {
		t.Fatalf("resize: %v", err)
	}
	got, err := e.blocks.Get(e.ctx, autoID)
	if err != nil || got == nil {
		t.Fatalf("get: %v %v", got, err)
	}
	wantStart := time.Date(2026, 4, 29, 9, 45, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 4, 29, 10, 45, 0, 0, time.UTC)
	if !got.StartTime.Equal(wantStart) {
		t.Errorf("start: want %v got %v", wantStart, got.StartTime)
	}
	if got.EndTime == nil || !got.EndTime.Equal(wantEnd) {
		t.Errorf("end: want %v got %v", wantEnd, got.EndTime)
	}
	if !got.IsManual {
		t.Error("auto block should be promoted to manual after resize")
	}

	// Resize that would step into the 11:00-12:00 neighbor must fail.
	collide := time.Date(2026, 4, 29, 11, 30, 0, 0, time.UTC)
	if err := e.orch.ResizeBlock(e.ctx, autoID, wantStart, collide); err == nil {
		t.Error("expected overlap rejection")
	}
	got2, _ := e.blocks.Get(e.ctx, autoID)
	if got2 == nil || got2.EndTime == nil || !got2.EndTime.Equal(wantEnd) {
		t.Errorf("block changed after rejected resize: %+v", got2)
	}
}

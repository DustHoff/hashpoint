package tracker

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/onesi/hashpoint/internal/storage"
	"github.com/onesi/hashpoint/internal/winapi"
)

// fakeSource is a deterministic FocusSource for tests.
type fakeSource struct {
	mu    sync.Mutex
	info  winapi.FocusInfo
	idle  time.Duration
	calls int
}

func (f *fakeSource) set(info winapi.FocusInfo, idle time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.info = info
	f.idle = idle
}

func (f *fakeSource) Foreground() (winapi.FocusInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.info, nil
}

func (f *fakeSource) IdleDuration() (time.Duration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.idle, nil
}

// fakeClock returns a controllable time.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func TestTracker_OpensAndClosesBlockOnSwitch(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db, err := storage.OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	blocks := storage.NewFocusBlockRepo(db)
	rules := storage.NewRuleRepo(db)
	src := &fakeSource{}
	clock := &fakeClock{t: time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)}
	src.set(winapi.FocusInfo{ProcessName: "chrome.exe", Title: "GitHub"}, 0)

	trk := New(Config{PollInterval: time.Millisecond, IdleThreshold: 5 * time.Minute},
		blocks, rules, nil, WithFocusSource(src), WithClock(clock))

	// First tick: opens a block.
	trk.tick(ctx)
	open, err := blocks.LastOpen(ctx)
	if err != nil {
		t.Fatalf("last open: %v", err)
	}
	if open == nil {
		t.Fatal("expected an open block after first tick")
	}
	if open.ProcessName != "chrome.exe" || open.WindowTitle != "GitHub" {
		t.Fatalf("wrong block: %+v", open)
	}

	// Switch focus.
	clock.advance(30 * time.Second)
	src.set(winapi.FocusInfo{ProcessName: "code.exe", Title: "main.go"}, 0)
	trk.tick(ctx)

	// The chrome block should now be closed.
	prev, err := blocks.Get(ctx, open.ID)
	if err != nil {
		t.Fatalf("get prev: %v", err)
	}
	if prev.EndTime == nil {
		t.Fatal("expected previous block to be closed")
	}
	if prev.DurationSec == 0 {
		t.Errorf("duration should be > 0")
	}

	open2, err := blocks.LastOpen(ctx)
	if err != nil || open2 == nil {
		t.Fatalf("expected new open block, err=%v", err)
	}
	if open2.ProcessName != "code.exe" {
		t.Fatalf("wrong block: %+v", open2)
	}
}

func TestTracker_MarksIdleWhenThresholdExceeded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := storage.OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	blocks := storage.NewFocusBlockRepo(db)
	rules := storage.NewRuleRepo(db)
	src := &fakeSource{}
	clock := &fakeClock{t: time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)}
	src.set(winapi.FocusInfo{ProcessName: "code.exe", Title: "main.go"}, 0)

	trk := New(Config{PollInterval: time.Millisecond, IdleThreshold: 5 * time.Minute},
		blocks, rules, nil, WithFocusSource(src), WithClock(clock))

	trk.tick(ctx)
	open, _ := blocks.LastOpen(ctx)
	if open == nil {
		t.Fatal("expected an open block")
	}

	clock.advance(6 * time.Minute)
	src.set(winapi.FocusInfo{ProcessName: "code.exe", Title: "main.go"}, 6*time.Minute)
	trk.tick(ctx)

	got, err := blocks.Get(ctx, open.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.IsIdle {
		t.Fatal("expected block to be marked idle")
	}
	if got.EndTime == nil {
		t.Fatal("expected block to be closed")
	}
}

func TestTracker_ManualTagOverridesAutoTagRules(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := storage.OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	blocks := storage.NewFocusBlockRepo(db)
	tags := storage.NewTagRepo(db)
	rules := storage.NewRuleRepo(db)

	autoTag := storage.Tag{Name: "#auto"}
	if err := tags.Create(ctx, &autoTag); err != nil {
		t.Fatalf("create auto tag: %v", err)
	}
	manualTag := storage.Tag{Name: "#manual"}
	if err := tags.Create(ctx, &manualTag); err != nil {
		t.Fatalf("create manual tag: %v", err)
	}
	rule := storage.Rule{
		MatchField: "process_name",
		MatchType:  "contains",
		Pattern:    "chrome",
		TagID:      autoTag.ID,
		Priority:   1,
		Enabled:    true,
	}
	if err := rules.Create(ctx, &rule); err != nil {
		t.Fatalf("create rule: %v", err)
	}

	src := &fakeSource{}
	clock := &fakeClock{t: time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)}
	src.set(winapi.FocusInfo{ProcessName: "chrome.exe", Title: "GitHub"}, 0)

	trk := New(Config{PollInterval: time.Millisecond, IdleThreshold: 5 * time.Minute},
		blocks, rules, nil, WithFocusSource(src), WithClock(clock))

	// Without manual mode the rule engine drives tagging.
	trk.tick(ctx)
	first, _ := blocks.LastOpen(ctx)
	if first == nil || first.TagID == nil || *first.TagID != autoTag.ID || !first.AutoTagged {
		t.Fatalf("expected auto-tagged block, got %+v", first)
	}

	// Engage manual mode — tracker keeps polling, but new blocks inherit
	// the manual tag instead of going through the rules. Process tracking
	// and the rule engine must not be torn down (no Pause), they're just
	// outvoted by the explicit user choice.
	trk.SetManualTag(&manualTag.ID)
	clock.advance(30 * time.Second)
	src.set(winapi.FocusInfo{ProcessName: "chrome.exe", Title: "Inbox"}, 0)
	trk.tick(ctx)

	second, _ := blocks.LastOpen(ctx)
	if second == nil || second.ID == first.ID {
		t.Fatalf("expected a new block while manual is active, got %+v", second)
	}
	if second.TagID == nil || *second.TagID != manualTag.ID {
		t.Fatalf("manual tag must override rule engine, got tag %v", second.TagID)
	}
	if second.AutoTagged {
		t.Errorf("manual-overridden blocks must not be flagged auto_tagged")
	}

	// Clearing the manual tag puts the rule engine back in charge.
	trk.SetManualTag(nil)
	clock.advance(30 * time.Second)
	src.set(winapi.FocusInfo{ProcessName: "chrome.exe", Title: "Calendar"}, 0)
	trk.tick(ctx)

	third, _ := blocks.LastOpen(ctx)
	if third == nil || third.ID == second.ID {
		t.Fatalf("expected a new block after clearing manual, got %+v", third)
	}
	if third.TagID == nil || *third.TagID != autoTag.ID || !third.AutoTagged {
		t.Errorf("rule engine must drive tagging again, got %+v", third)
	}
}

func TestTracker_GranularitySnapsToGrid(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db, err := storage.OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	blocks := storage.NewFocusBlockRepo(db)
	rules := storage.NewRuleRepo(db)
	src := &fakeSource{}
	// 09:07:13 local — picked so floor(09:07, 15min) = 09:00 and the test
	// asserts both bounds get snapped to grid, independent of host TZ.
	start := time.Date(2026, 4, 25, 9, 7, 13, 0, time.Local)
	clock := &fakeClock{t: start}
	src.set(winapi.FocusInfo{ProcessName: "chrome.exe", Title: "GitHub"}, 0)

	trk := New(Config{
		PollInterval:        time.Millisecond,
		IdleThreshold:       5 * time.Minute,
		TagBlockGranularity: 15 * time.Minute,
	}, blocks, rules, nil, WithFocusSource(src), WithClock(clock))

	trk.tick(ctx)
	first, err := blocks.LastOpen(ctx)
	if err != nil || first == nil {
		t.Fatalf("expected an open block, err=%v", err)
	}
	wantStart := time.Date(2026, 4, 25, 9, 0, 0, 0, time.Local)
	if !first.StartTime.Equal(wantStart.UTC()) {
		t.Errorf("first block start = %v, want %v (floored to 15-min grid)", first.StartTime, wantStart.UTC())
	}

	// Switch focus 5 minutes later — the prev block must close on the next
	// 15-min boundary (09:15) and the new block must start there too.
	clock.advance(5 * time.Minute)
	src.set(winapi.FocusInfo{ProcessName: "code.exe", Title: "main.go"}, 0)
	trk.tick(ctx)

	closedFirst, _ := blocks.Get(ctx, first.ID)
	if closedFirst.EndTime == nil {
		t.Fatal("expected first block to be closed")
	}
	wantEnd := time.Date(2026, 4, 25, 9, 15, 0, 0, time.Local)
	if !closedFirst.EndTime.Equal(wantEnd.UTC()) {
		t.Errorf("first block end = %v, want %v (ceil to next 15-min slot)", *closedFirst.EndTime, wantEnd.UTC())
	}

	second, _ := blocks.LastOpen(ctx)
	if second == nil || second.ID == first.ID {
		t.Fatalf("expected new block on focus switch, got %+v", second)
	}
	if !second.StartTime.Equal(wantEnd.UTC()) {
		t.Errorf("second block start = %v, want %v (== prev end on grid)", second.StartTime, wantEnd.UTC())
	}
}

func TestTracker_SameFocusKeepsBlockOpen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := storage.OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	blocks := storage.NewFocusBlockRepo(db)
	rules := storage.NewRuleRepo(db)
	src := &fakeSource{}
	clock := &fakeClock{t: time.Now().UTC()}
	src.set(winapi.FocusInfo{ProcessName: "code.exe", Title: "main.go"}, 0)

	trk := New(Config{PollInterval: time.Millisecond, IdleThreshold: 5 * time.Minute},
		blocks, rules, nil, WithFocusSource(src), WithClock(clock))

	trk.tick(ctx)
	open1, _ := blocks.LastOpen(ctx)

	clock.advance(2 * time.Second)
	trk.tick(ctx)
	open2, _ := blocks.LastOpen(ctx)

	if open1 == nil || open2 == nil {
		t.Fatal("expected both ticks to leave a block open")
	}
	if open1.ID != open2.ID {
		t.Fatalf("expected same block, got %d -> %d", open1.ID, open2.ID)
	}
}

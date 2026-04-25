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

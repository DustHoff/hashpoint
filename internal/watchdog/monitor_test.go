package watchdog_test

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/dusthoff/hashpoint/internal/crashguard"
	"github.com/dusthoff/hashpoint/internal/watchdog"
)

type aliveResult struct {
	alive     bool
	lastAlive time.Time
	err       error
}

type fakeProbe struct {
	target watchdog.Target

	mu            sync.Mutex
	seq           []aliveResult
	idx           int
	relaunchCount int
	relaunchErr   error
	onRelaunch    func()
}

func (p *fakeProbe) ResolveTarget(context.Context) (watchdog.Target, error) {
	return p.target, nil
}

func (p *fakeProbe) CollectorAlive(context.Context, watchdog.Target) (bool, time.Time, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.seq[len(p.seq)-1]
	if p.idx < len(p.seq) {
		r = p.seq[p.idx]
		p.idx++
	}
	return r.alive, r.lastAlive, r.err
}

func (p *fakeProbe) Relaunch(context.Context, watchdog.Target) error {
	p.mu.Lock()
	p.relaunchCount++
	cb := p.onRelaunch
	err := p.relaunchErr
	p.mu.Unlock()
	if cb != nil {
		cb()
	}
	return err
}

func (p *fakeProbe) relaunches() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.relaunchCount
}

type fakeCloser struct {
	mu    sync.Mutex
	calls []time.Time
}

func (c *fakeCloser) close(context.Context, watchdog.Target, time.Time) (int, error) {
	return 1, nil
}

func (c *fakeCloser) record(_ context.Context, _ watchdog.Target, at time.Time) (int, error) {
	c.mu.Lock()
	c.calls = append(c.calls, at)
	c.mu.Unlock()
	return 1, nil
}

func (c *fakeCloser) first() (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.calls) == 0 {
		return time.Time{}, false
	}
	return c.calls[0], true
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fastTiming polls and backs off on millisecond scale so tests run quickly.
func fastTiming() watchdog.Option {
	return watchdog.WithTiming(time.Millisecond, time.Millisecond, 2*time.Millisecond, time.Minute, 5)
}

func TestMonitor_DeadCollector_ClosesAtLastAliveAndRelaunches(t *testing.T) {
	// Marker far in the past so pickLastAlive returns it verbatim regardless of
	// the test clock (it is never after "now").
	marker := time.Date(2020, 1, 1, 9, 0, 0, 0, time.UTC)
	relaunched := make(chan struct{}, 1)
	p := &fakeProbe{
		target: watchdog.Target{DBFile: "db", Exe: "hashpoint.exe"},
		seq:    []aliveResult{{alive: false, lastAlive: marker}},
		onRelaunch: func() {
			select {
			case relaunched <- struct{}{}:
			default:
			}
		},
	}
	c := &fakeCloser{}
	m := watchdog.New(p, c.record, discardLogger(), fastTiming())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = m.Run(ctx) }()

	select {
	case <-relaunched:
	case <-time.After(2 * time.Second):
		t.Fatal("collector was not relaunched after an unclean death")
	}
	cancel()

	at, ok := c.first()
	if !ok {
		t.Fatal("open tag-blocks were not closed before relaunch")
	}
	if !at.Equal(marker) {
		t.Fatalf("closed at %v, want last-alive %v", at, marker)
	}
}

func TestMonitor_AbsentMarker_RespectsCleanQuit(t *testing.T) {
	p := &fakeProbe{seq: []aliveResult{{err: crashguard.ErrMarkerAbsent}}}
	c := &fakeCloser{}
	m := watchdog.New(p, c.record, discardLogger(), fastTiming())

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	_ = m.Run(ctx)

	if got := p.relaunches(); got != 0 {
		t.Fatalf("relaunched %d times after a clean quit, want 0", got)
	}
	if _, ok := c.first(); ok {
		t.Fatal("closed tag-blocks after a clean quit")
	}
}

func TestMonitor_CorruptMarker_SkipsTick(t *testing.T) {
	p := &fakeProbe{seq: []aliveResult{{err: crashguard.ErrMarkerCorrupt}}}
	c := &fakeCloser{}
	m := watchdog.New(p, c.close, discardLogger(), fastTiming())

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	_ = m.Run(ctx)

	if got := p.relaunches(); got != 0 {
		t.Fatalf("relaunched %d times on a torn marker, want 0", got)
	}
}

func TestMonitor_AliveCollector_NoAction(t *testing.T) {
	p := &fakeProbe{seq: []aliveResult{{alive: true, lastAlive: time.Now()}}}
	c := &fakeCloser{}
	m := watchdog.New(p, c.record, discardLogger(), fastTiming())

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	_ = m.Run(ctx)

	if got := p.relaunches(); got != 0 {
		t.Fatalf("relaunched %d times while collector alive, want 0", got)
	}
	if _, ok := c.first(); ok {
		t.Fatal("closed tag-blocks while collector alive")
	}
}

package plugin

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClient is a clientHandle implementation that lets a test flip
// Exited() at will without involving a real subprocess.
type fakeClient struct {
	exited atomic.Bool
	killed atomic.Bool
}

func (f *fakeClient) Kill() {
	f.killed.Store(true)
	f.exited.Store(true)
}

func (f *fakeClient) Exited() bool { return f.exited.Load() }

// stateRecorder captures every OnStateChanged callback so tests can
// assert what the host emitted.
type stateRecorder struct {
	mu      sync.Mutex
	seen    []Info
	notifCh chan Info
}

func newStateRecorder() *stateRecorder {
	return &stateRecorder{notifCh: make(chan Info, 8)}
}

func (r *stateRecorder) record(info Info) {
	r.mu.Lock()
	r.seen = append(r.seen, info)
	r.mu.Unlock()
	select {
	case r.notifCh <- info:
	default:
	}
}

func (r *stateRecorder) wait(t *testing.T, d time.Duration) (Info, bool) {
	t.Helper()
	select {
	case info := <-r.notifCh:
		return info, true
	case <-time.After(d):
		return Info{}, false
	}
}

// newWatcherTestHost builds a minimal host wired with a stateRecorder
// and a fast exit poll, so tests do not have to wait out the 2s
// production cadence.
func newWatcherTestHost(t *testing.T) (*Host, *stateRecorder) {
	t.Helper()
	rec := newStateRecorder()
	h := NewHost(HostDeps{
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		Settings:       newFakeSettings(),
		OnStateChanged: rec.record,
	})
	h.exitPollInterval = 5 * time.Millisecond
	t.Cleanup(func() { _ = h.Stop(context.Background()) })
	return h, rec
}

// installRunningPlugin inserts a synthetic StateRunning plugin into the
// host's map so tests can spawn a watcher against it without going
// through launch() (which would need a real subprocess binary).
func installRunningPlugin(h *Host, name string, cli clientHandle) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.plugins[name] = &pluginInstance{
		name:     name,
		manifest: &Manifest{Name: name, Version: "1.0.0", APIVersion: 1},
		state:    StateRunning,
		client:   cli,
	}
}

func TestWatchExit_TransitionsOnUnexpectedExit(t *testing.T) {
	h, rec := newWatcherTestHost(t)
	cli := &fakeClient{}
	installRunningPlugin(h, "alpha", cli)

	go h.watchExit("alpha", cli)

	cli.exited.Store(true)
	info, ok := rec.wait(t, time.Second)
	if !ok {
		t.Fatal("OnStateChanged was not called after unexpected exit")
	}
	if info.State != StateFailed {
		t.Errorf("notified state = %q, want %q", info.State, StateFailed)
	}
	if info.LastError == "" {
		t.Errorf("notified LastError empty; want non-empty crash message")
	}

	// And the host's view should match the notification.
	got, ok := h.Get("alpha")
	if !ok {
		t.Fatal("plugin alpha vanished from host")
	}
	if got.State != StateFailed {
		t.Errorf("host state = %q, want %q", got.State, StateFailed)
	}
}

func TestWatchExit_BailsWhenClientWasReplaced(t *testing.T) {
	h, rec := newWatcherTestHost(t)
	oldCli := &fakeClient{}
	newCli := &fakeClient{}
	installRunningPlugin(h, "alpha", oldCli)

	go h.watchExit("alpha", oldCli)

	// Reload-style swap: while the old watcher is still polling, the
	// instance acquires a brand-new client. The next tick's Exited()
	// will trip (because the old subprocess is gone) but the watcher
	// must NOT clobber the freshly installed instance.
	h.mu.Lock()
	h.plugins["alpha"].client = newCli
	h.mu.Unlock()
	oldCli.exited.Store(true)

	if _, ok := rec.wait(t, 200*time.Millisecond); ok {
		t.Error("OnStateChanged fired for a stale watcher whose client was replaced")
	}

	got, ok := h.Get("alpha")
	if !ok {
		t.Fatal("plugin alpha vanished from host")
	}
	if got.State != StateRunning {
		t.Errorf("host state = %q, want %q (replaced client should still look running)",
			got.State, StateRunning)
	}
}

func TestWatchExit_BailsWhenPluginRemoved(t *testing.T) {
	h, rec := newWatcherTestHost(t)
	cli := &fakeClient{}
	installRunningPlugin(h, "alpha", cli)

	go h.watchExit("alpha", cli)

	// Uninstall-style removal during a crash race.
	h.mu.Lock()
	delete(h.plugins, "alpha")
	h.mu.Unlock()
	cli.exited.Store(true)

	if _, ok := rec.wait(t, 200*time.Millisecond); ok {
		t.Error("OnStateChanged fired for a removed plugin")
	}
}

func TestWatchExit_ExitsOnHostStop(t *testing.T) {
	h, rec := newWatcherTestHost(t)
	cli := &fakeClient{}
	installRunningPlugin(h, "alpha", cli)

	done := make(chan struct{})
	go func() {
		h.watchExit("alpha", cli)
		close(done)
	}()

	// Stop cancels bgCtx; watcher must return without firing any
	// notification (cli has not Exited).
	if err := h.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watcher did not return after host Stop")
	}

	if _, ok := rec.wait(t, 50*time.Millisecond); ok {
		t.Error("OnStateChanged fired on Stop-driven exit")
	}
}

func TestWatchExit_NoCallbackWhenDepsCallbackNil(t *testing.T) {
	h := NewHost(HostDeps{
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Settings: newFakeSettings(),
	})
	h.exitPollInterval = 5 * time.Millisecond
	t.Cleanup(func() { _ = h.Stop(context.Background()) })

	cli := &fakeClient{}
	installRunningPlugin(h, "alpha", cli)

	done := make(chan struct{})
	go func() {
		h.watchExit("alpha", cli)
		close(done)
	}()
	cli.exited.Store(true)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watcher did not return after exit detected")
	}

	// Host state must still transition even without a notifier wired up
	// — UI refreshes via plugin_list polling still need accurate state.
	got, ok := h.Get("alpha")
	if !ok {
		t.Fatal("plugin alpha vanished from host")
	}
	if got.State != StateFailed {
		t.Errorf("state = %q, want %q", got.State, StateFailed)
	}
}

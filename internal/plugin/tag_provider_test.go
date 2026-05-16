package plugin

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dusthoff/hashpoint/plugin/sdk"
)

// fakeTagProvider is a scripted TagProviderHandler for fan-out tests.
// ListTags is unused by these tests but the host's interface assertion
// requires it; the script lets a test fail it on demand.
type fakeTagProvider struct {
	tags       []sdk.ImportedTag
	tagsErr    error
	orders     []sdk.Order
	ordersErr  error
	listOrders int

	// notify* configure NotifyTagOrders. notifyDelay forces a sleep
	// (respects ctx.Done so a timeout cancel returns promptly).
	// notifyErr is returned after the (delayed) call body runs.
	// notifyArgs records every snapshot received under notifyMu.
	// notifyWG (when set) gets Done() in every call so a test can
	// wait for the fire-and-forget fan-out to complete.
	notifyDelay time.Duration
	notifyErr   error
	notifyMu    sync.Mutex
	notifyArgs  [][]sdk.TagOrderMapping
	notifyCalls atomic.Int32
	notifyWG    *sync.WaitGroup
}

func (f *fakeTagProvider) ListTags(_ context.Context) ([]sdk.ImportedTag, error) {
	return f.tags, f.tagsErr
}

func (f *fakeTagProvider) ListOrders(_ context.Context) ([]sdk.Order, error) {
	f.listOrders++
	return f.orders, f.ordersErr
}

func (f *fakeTagProvider) NotifyTagOrders(ctx context.Context, mappings []sdk.TagOrderMapping) error {
	defer func() {
		if f.notifyWG != nil {
			f.notifyWG.Done()
		}
	}()
	if f.notifyDelay > 0 {
		select {
		case <-time.After(f.notifyDelay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f.notifyMu.Lock()
	clone := append([]sdk.TagOrderMapping(nil), mappings...)
	f.notifyArgs = append(f.notifyArgs, clone)
	f.notifyMu.Unlock()
	f.notifyCalls.Add(1)
	return f.notifyErr
}

// snapshot returns a deterministic copy of every snapshot the fake
// received, in arrival order, for assertion.
func (f *fakeTagProvider) snapshot() [][]sdk.TagOrderMapping {
	f.notifyMu.Lock()
	defer f.notifyMu.Unlock()
	out := make([][]sdk.TagOrderMapping, len(f.notifyArgs))
	for i, s := range f.notifyArgs {
		out[i] = append([]sdk.TagOrderMapping(nil), s...)
	}
	return out
}

// withRunningTagProvider injects a tag_provider plugin in StateRunning.
func withRunningTagProvider(h *Host, name string, handler sdk.TagProviderHandler) {
	h.plugins[name] = &pluginInstance{
		name:        name,
		state:       StateRunning,
		tagProvider: handler,
		manifest:    &Manifest{Name: name, Capabilities: []string{string(sdk.CapTagProvider)}},
		meta:        sdk.Metadata{Name: name},
	}
}

func TestListAllOrders_NoPlugins(t *testing.T) {
	h := quietHost(t, newFakeSettings(), t.TempDir())
	if got := h.ListAllOrders(context.Background()); got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestListAllOrders_MergesAndSortsByPluginName(t *testing.T) {
	h := quietHost(t, newFakeSettings(), t.TempDir())
	bravo := &fakeTagProvider{orders: []sdk.Order{
		{ID: "B-1", Name: "Bravo-Auftrag-1"},
		{ID: "B-2", Name: "Bravo-Auftrag-2"},
	}}
	alpha := &fakeTagProvider{orders: []sdk.Order{
		{ID: "A-1", Name: "Alpha-Auftrag-1"},
	}}
	withRunningTagProvider(h, "bravo", bravo)
	withRunningTagProvider(h, "alpha", alpha)

	got := h.ListAllOrders(context.Background())
	if len(got) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(got))
	}
	if got[0].PluginName != "alpha" || got[1].PluginName != "bravo" {
		t.Errorf("groups not sorted: %s, %s", got[0].PluginName, got[1].PluginName)
	}
	if len(got[0].Orders) != 1 || got[0].Orders[0].Name != "Alpha-Auftrag-1" {
		t.Errorf("alpha orders mismatch: %+v", got[0].Orders)
	}
	if len(got[1].Orders) != 2 {
		t.Errorf("bravo orders count = %d, want 2", len(got[1].Orders))
	}
	if alpha.listOrders != 1 || bravo.listOrders != 1 {
		t.Errorf("expected each plugin called once; alpha=%d bravo=%d",
			alpha.listOrders, bravo.listOrders)
	}
}

func TestListAllOrders_DropsFailingPlugin(t *testing.T) {
	h := quietHost(t, newFakeSettings(), t.TempDir())
	good := &fakeTagProvider{orders: []sdk.Order{{ID: "1", Name: "ok"}}}
	bad := &fakeTagProvider{ordersErr: errors.New("rpc kaboom")}
	withRunningTagProvider(h, "good", good)
	withRunningTagProvider(h, "bad", bad)

	got := h.ListAllOrders(context.Background())
	if len(got) != 1 {
		t.Fatalf("expected 1 group (failing plugin dropped), got %d", len(got))
	}
	if got[0].PluginName != "good" {
		t.Errorf("kept plugin = %q, want %q", got[0].PluginName, "good")
	}
}

func TestListAllOrders_SkipsNonRunning(t *testing.T) {
	h := quietHost(t, newFakeSettings(), t.TempDir())
	handler := &fakeTagProvider{orders: []sdk.Order{{ID: "x", Name: "should not appear"}}}
	// Inject in StateFailed — handler is present but state != running.
	h.plugins["broken"] = &pluginInstance{
		name:        "broken",
		state:       StateFailed,
		tagProvider: handler,
		manifest:    &Manifest{Name: "broken", Capabilities: []string{string(sdk.CapTagProvider)}},
	}

	if got := h.ListAllOrders(context.Background()); got != nil {
		t.Errorf("expected nil (no running plugins), got %+v", got)
	}
	if handler.listOrders != 0 {
		t.Errorf("ListOrders must not be called on non-running plugins; got %d", handler.listOrders)
	}
}

func TestNotifyTagOrdersChanged_NoPlugins(t *testing.T) {
	h := quietHost(t, newFakeSettings(), t.TempDir())
	// Should not panic, should not block — just return.
	h.NotifyTagOrdersChanged([]sdk.TagOrderMapping{{TagPath: "x", OrderName: "y"}})
}

func TestNotifyTagOrdersChanged_FansOutToEveryRunning(t *testing.T) {
	h := quietHost(t, newFakeSettings(), t.TempDir())
	var wg sync.WaitGroup
	wg.Add(2)
	alpha := &fakeTagProvider{notifyWG: &wg}
	bravo := &fakeTagProvider{notifyWG: &wg}
	withRunningTagProvider(h, "alpha", alpha)
	withRunningTagProvider(h, "bravo", bravo)

	snapshot := []sdk.TagOrderMapping{
		{TagPath: "personio/projektA", OrderName: "Auftrag-42"},
		{TagPath: "internal/admin", OrderName: ""},
	}
	h.NotifyTagOrdersChanged(snapshot)
	waitOrFatal(t, &wg, time.Second)

	for _, tc := range []struct {
		name string
		p    *fakeTagProvider
	}{{"alpha", alpha}, {"bravo", bravo}} {
		if got := tc.p.notifyCalls.Load(); got != 1 {
			t.Errorf("%s NotifyTagOrders calls = %d, want 1", tc.name, got)
		}
		got := tc.p.snapshot()
		if len(got) != 1 || len(got[0]) != len(snapshot) {
			t.Fatalf("%s snapshot shape unexpected: %+v", tc.name, got)
		}
		for i, m := range got[0] {
			if m != snapshot[i] {
				t.Errorf("%s snapshot[%d] = %+v, want %+v", tc.name, i, m, snapshot[i])
			}
		}
	}
}

func TestNotifyTagOrdersChanged_SkipsNonRunning(t *testing.T) {
	h := quietHost(t, newFakeSettings(), t.TempDir())
	var wg sync.WaitGroup
	wg.Add(1)
	live := &fakeTagProvider{notifyWG: &wg}
	dead := &fakeTagProvider{}
	withRunningTagProvider(h, "live", live)
	h.plugins["dead"] = &pluginInstance{
		name:        "dead",
		state:       StateFailed,
		tagProvider: dead,
		manifest:    &Manifest{Name: "dead", Capabilities: []string{string(sdk.CapTagProvider)}},
	}

	h.NotifyTagOrdersChanged([]sdk.TagOrderMapping{{TagPath: "x", OrderName: "y"}})
	waitOrFatal(t, &wg, time.Second)

	if got := live.notifyCalls.Load(); got != 1 {
		t.Errorf("live NotifyTagOrders calls = %d, want 1", got)
	}
	if got := dead.notifyCalls.Load(); got != 0 {
		t.Errorf("dead plugin NotifyTagOrders calls = %d, want 0 (state=failed)", got)
	}
}

func TestNotifyTagOrdersChanged_PluginErrorDoesNotAffectOthers(t *testing.T) {
	h := quietHost(t, newFakeSettings(), t.TempDir())
	var wg sync.WaitGroup
	wg.Add(2)
	good := &fakeTagProvider{notifyWG: &wg}
	bad := &fakeTagProvider{notifyWG: &wg, notifyErr: errors.New("rpc kaboom")}
	withRunningTagProvider(h, "good", good)
	withRunningTagProvider(h, "bad", bad)

	h.NotifyTagOrdersChanged([]sdk.TagOrderMapping{{TagPath: "x", OrderName: "y"}})
	waitOrFatal(t, &wg, time.Second)

	if got := good.notifyCalls.Load(); got != 1 {
		t.Errorf("good NotifyTagOrders calls = %d, want 1 (must be unaffected by sibling's error)", got)
	}
	if got := bad.notifyCalls.Load(); got != 1 {
		t.Errorf("bad NotifyTagOrders calls = %d, want 1 (the call still ran; only its error was dropped)", got)
	}
}

func TestNotifyTagOrdersChanged_TimeoutDropsSnapshotForSlowPlugin(t *testing.T) {
	h := quietHost(t, newFakeSettings(), t.TempDir())
	h.deps.SubmitTimeout = 10 * time.Millisecond
	var wg sync.WaitGroup
	wg.Add(2)
	fast := &fakeTagProvider{notifyWG: &wg}
	slow := &fakeTagProvider{notifyWG: &wg, notifyDelay: 500 * time.Millisecond}
	withRunningTagProvider(h, "fast", fast)
	withRunningTagProvider(h, "slow", slow)

	h.NotifyTagOrdersChanged([]sdk.TagOrderMapping{{TagPath: "x", OrderName: "y"}})
	waitOrFatal(t, &wg, time.Second)

	if got := fast.notifyCalls.Load(); got != 1 {
		t.Errorf("fast NotifyTagOrders calls = %d, want 1", got)
	}
	// The slow plugin's call body bails out on ctx.Done() before
	// reaching the recorder, so notifyCalls stays at 0 — exactly the
	// "snapshot dropped for this plugin" the host docs guarantee.
	if got := slow.notifyCalls.Load(); got != 0 {
		t.Errorf("slow plugin notifyCalls = %d, want 0 (snapshot must be dropped on timeout)", got)
	}
}

// waitOrFatal waits for wg with a hard timeout so a missing Done() in
// the production path fails the test instead of deadlocking it.
func waitOrFatal(t *testing.T, wg *sync.WaitGroup, timeout time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatalf("fan-out did not complete within %s", timeout)
	}
}

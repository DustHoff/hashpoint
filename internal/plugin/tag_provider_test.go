package plugin

import (
	"context"
	"errors"
	"testing"

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
}

func (f *fakeTagProvider) ListTags(_ context.Context) ([]sdk.ImportedTag, error) {
	return f.tags, f.tagsErr
}

func (f *fakeTagProvider) ListOrders(_ context.Context) ([]sdk.Order, error) {
	f.listOrders++
	return f.orders, f.ordersErr
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

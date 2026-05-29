package plugin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// fakeApprovalStore is an in-memory ApprovalStore for the gating tests.
type fakeApprovalStore struct {
	approved map[string]bool
	err      error
}

func (f *fakeApprovalStore) IsApproved(_ context.Context, name string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return f.approved[name], nil
}

func (f *fakeApprovalStore) Approve(_ context.Context, name string) error {
	if f.err != nil {
		return f.err
	}
	if f.approved == nil {
		f.approved = map[string]bool{}
	}
	f.approved[name] = true
	return nil
}

func (f *fakeApprovalStore) ListApproved(_ context.Context) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []string
	for n, ok := range f.approved {
		if ok {
			out = append(out, n)
		}
	}
	return out, nil
}

// TestLaunchParksUnapprovedPluginPending verifies the opt-in gate: an
// unapproved plugin directory is parked in StatePending and its subprocess
// is never started.
func TestLaunchParksUnapprovedPluginPending(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sideloaded"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	h := NewHost(HostDeps{
		PluginsDir:        dir,
		ApprovalStore:     &fakeApprovalStore{approved: map[string]bool{}},
		DiscoveryInterval: -1,
	})
	if err := h.launch(context.Background(), "sideloaded"); err != nil {
		t.Fatalf("launch: %v", err)
	}
	info, ok := h.Get("sideloaded")
	if !ok {
		t.Fatal("plugin not recorded")
	}
	if info.State != StatePending {
		t.Errorf("state = %q, want %q", info.State, StatePending)
	}
}

// TestIsApprovedNilStoreApprovesAll verifies the backward-compatible default:
// with no ApprovalStore wired, every plugin is approved (so minimal hosts and
// existing tests keep launching plugins).
func TestIsApprovedNilStoreApprovesAll(t *testing.T) {
	h := NewHost(HostDeps{})
	if !h.isApproved(context.Background(), "anything") {
		t.Error("nil ApprovalStore must approve every plugin (backward compat)")
	}
}

// TestIsApprovedStoreErrorFailsSafe verifies a store error parks the plugin
// pending rather than launching it.
func TestIsApprovedStoreErrorFailsSafe(t *testing.T) {
	h := NewHost(HostDeps{ApprovalStore: &fakeApprovalStore{err: errors.New("boom")}})
	if h.isApproved(context.Background(), "x") {
		t.Error("a store error must fail safe to NOT approved")
	}
}

// TestLaunchRejectsInvalidName verifies the path-traversal guard fires before
// any path is built or binary launched.
func TestLaunchRejectsInvalidName(t *testing.T) {
	h := NewHost(HostDeps{PluginsDir: t.TempDir(), DiscoveryInterval: -1})
	const bad = `..\evil`
	err := h.launch(context.Background(), bad)
	if !errors.Is(err, ErrInvalidPluginName) {
		t.Fatalf("expected ErrInvalidPluginName, got %v", err)
	}
	if info, ok := h.Get(bad); !ok || info.State != StateFailed {
		t.Errorf("invalid-name plugin should be recorded failed, got ok=%v state=%q", ok, info.State)
	}
}

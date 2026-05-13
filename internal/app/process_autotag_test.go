package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"

	pluginhost "github.com/dusthoff/hashpoint/internal/plugin"
	"github.com/dusthoff/hashpoint/internal/storage"
)

// fakeHostResolver is a scripted hostAutoTagResolver for adapter tests.
type fakeHostResolver struct {
	resolve func(processName, windowTitle string, isComm bool) *pluginhost.ProcessAutoTagResolution
	calls   atomic.Int32
}

func (f *fakeHostResolver) ResolveProcessAutoTag(_ context.Context, p, w string, isComm bool) *pluginhost.ProcessAutoTagResolution {
	f.calls.Add(1)
	if f.resolve == nil {
		return nil
	}
	return f.resolve(p, w, isComm)
}

// fakeTagRepo is a minimal storage.TagRepository for adapter tests.
// Only EnsureByPath is meaningfully exercised; the rest of the surface
// returns zero values.
type fakeTagRepo struct {
	ensure     func(path string) (*storage.Tag, error)
	ensureCall atomic.Int32
}

func (f *fakeTagRepo) Create(context.Context, *storage.Tag) error { return nil }
func (f *fakeTagRepo) Update(context.Context, *storage.Tag) error { return nil }
func (f *fakeTagRepo) Delete(context.Context, int64) error        { return nil }
func (f *fakeTagRepo) Get(context.Context, int64) (*storage.Tag, error) {
	return nil, nil
}
func (f *fakeTagRepo) List(context.Context) ([]storage.Tag, error)            { return nil, nil }
func (f *fakeTagRepo) Children(context.Context, int64) ([]storage.Tag, error) { return nil, nil }
func (f *fakeTagRepo) EnsureByPath(_ context.Context, path string) (*storage.Tag, error) {
	f.ensureCall.Add(1)
	if f.ensure == nil {
		return &storage.Tag{ID: 0, Name: path}, nil
	}
	return f.ensure(path)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestPluginAutoTagAdapter_NilHostReturnsNil(t *testing.T) {
	a := newPluginAutoTagAdapter(nil, &fakeTagRepo{}, discardLogger())
	got := a.Resolve(context.Background(), "code.exe", "", false)
	if got != nil {
		t.Errorf("expected nil with nil host, got %+v", got)
	}
}

func TestPluginAutoTagAdapter_NoMatchReturnsNil(t *testing.T) {
	host := &fakeHostResolver{
		resolve: func(_, _ string, _ bool) *pluginhost.ProcessAutoTagResolution { return nil },
	}
	repo := &fakeTagRepo{}
	a := newPluginAutoTagAdapter(host, repo, discardLogger())

	got := a.Resolve(context.Background(), "code.exe", "", false)
	if got != nil {
		t.Errorf("expected nil when host produced no match, got %+v", got)
	}
	if repo.ensureCall.Load() != 0 {
		t.Errorf("EnsureByPath must not be called on a no-match, got %d calls", repo.ensureCall.Load())
	}
}

func TestPluginAutoTagAdapter_ResolvesTagPath(t *testing.T) {
	host := &fakeHostResolver{
		resolve: func(_, _ string, _ bool) *pluginhost.ProcessAutoTagResolution {
			return &pluginhost.ProcessAutoTagResolution{
				PluginName:  "coder",
				TagName:     "productivity/coding",
				Description: "focus",
			}
		},
	}
	repo := &fakeTagRepo{
		ensure: func(path string) (*storage.Tag, error) {
			if path != "productivity/coding" {
				t.Errorf("EnsureByPath called with %q, want %q", path, "productivity/coding")
			}
			return &storage.Tag{ID: 42, Name: "#coding"}, nil
		},
	}
	a := newPluginAutoTagAdapter(host, repo, discardLogger())

	got := a.Resolve(context.Background(), "code.exe", "main.go", false)
	if got == nil {
		t.Fatal("expected resolution")
	}
	if got.PluginName != "coder" {
		t.Errorf("plugin = %q", got.PluginName)
	}
	if got.TagID != 42 {
		t.Errorf("tag id = %d, want 42", got.TagID)
	}
	if got.Description != "focus" {
		t.Errorf("description = %q", got.Description)
	}
}

func TestPluginAutoTagAdapter_CachesResolvedID(t *testing.T) {
	host := &fakeHostResolver{
		resolve: func(_, _ string, _ bool) *pluginhost.ProcessAutoTagResolution {
			return &pluginhost.ProcessAutoTagResolution{
				PluginName: "coder",
				TagName:    "productivity/coding",
			}
		},
	}
	repo := &fakeTagRepo{
		ensure: func(_ string) (*storage.Tag, error) {
			return &storage.Tag{ID: 7}, nil
		},
	}
	a := newPluginAutoTagAdapter(host, repo, discardLogger())

	// Three lookups; the second and third must hit the cache.
	for i := 0; i < 3; i++ {
		got := a.Resolve(context.Background(), "code.exe", "", false)
		if got == nil || got.TagID != 7 {
			t.Fatalf("iter %d: unexpected resolution: %+v", i, got)
		}
	}
	if got, want := repo.ensureCall.Load(), int32(1); got != want {
		t.Errorf("EnsureByPath calls = %d, want %d (cache miss only on first call)", got, want)
	}
}

func TestPluginAutoTagAdapter_EnsureErrorReturnsNil(t *testing.T) {
	host := &fakeHostResolver{
		resolve: func(_, _ string, _ bool) *pluginhost.ProcessAutoTagResolution {
			return &pluginhost.ProcessAutoTagResolution{
				PluginName: "broken",
				TagName:    "bogus",
			}
		},
	}
	repo := &fakeTagRepo{
		ensure: func(_ string) (*storage.Tag, error) {
			return nil, errors.New("disk full")
		},
	}
	a := newPluginAutoTagAdapter(host, repo, discardLogger())

	got := a.Resolve(context.Background(), "code.exe", "", false)
	if got != nil {
		t.Errorf("expected nil on ensure failure, got %+v", got)
	}
}

func TestPluginAutoTagAdapter_EmptyTagNameRejected(t *testing.T) {
	host := &fakeHostResolver{
		resolve: func(_, _ string, _ bool) *pluginhost.ProcessAutoTagResolution {
			return &pluginhost.ProcessAutoTagResolution{
				PluginName: "p",
				TagName:    "   ",
			}
		},
	}
	repo := &fakeTagRepo{}
	a := newPluginAutoTagAdapter(host, repo, discardLogger())

	got := a.Resolve(context.Background(), "code.exe", "", false)
	if got != nil {
		t.Errorf("expected nil on whitespace TagName, got %+v", got)
	}
	if repo.ensureCall.Load() != 0 {
		t.Errorf("EnsureByPath must not be called for whitespace tag, got %d", repo.ensureCall.Load())
	}
}

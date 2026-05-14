package plugin

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/dusthoff/hashpoint/plugin/sdk"
)

// fakeEntraSource is a scripted EntraTokenSource for hostapi tests.
type fakeEntraSource struct {
	acquire func(scopes []string, allowInteractive bool) (string, time.Time, error)
}

func (f *fakeEntraSource) AcquireToken(_ context.Context, scopes []string, allowInteractive bool) (string, time.Time, error) {
	if f.acquire == nil {
		return "", time.Time{}, errors.New("no script")
	}
	return f.acquire(scopes, allowInteractive)
}

func newBoundAPI(t *testing.T, source func() EntraTokenSource) *boundHostAPI {
	t.Helper()
	return &boundHostAPI{
		pluginName:  "test-plugin",
		log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		handles:     newHandleRegistry(),
		settings:    newFakeSettings(),
		entraSource: source,
	}
}

func TestRequestEntraToken_NoSourceReturnsErr(t *testing.T) {
	api := newBoundAPI(t, nil)
	_, _, err := api.RequestEntraToken(context.Background(), []string{"User.Read"})
	if !errors.Is(err, sdk.ErrEntraNotAvailable) {
		t.Errorf("expected ErrEntraNotAvailable, got %v", err)
	}
}

func TestRequestEntraToken_SourceReturnsNil(t *testing.T) {
	api := newBoundAPI(t, func() EntraTokenSource { return nil })
	_, _, err := api.RequestEntraToken(context.Background(), []string{"User.Read"})
	if !errors.Is(err, sdk.ErrEntraNotAvailable) {
		t.Errorf("expected ErrEntraNotAvailable, got %v", err)
	}
}

func TestRequestEntraToken_EmptyScopesReturnsErr(t *testing.T) {
	source := &fakeEntraSource{
		acquire: func(_ []string, _ bool) (string, time.Time, error) {
			t.Fatal("AcquireToken must not be called for empty scopes")
			return "", time.Time{}, nil
		},
	}
	api := newBoundAPI(t, func() EntraTokenSource { return source })
	_, _, err := api.RequestEntraToken(context.Background(), nil)
	if !errors.Is(err, sdk.ErrEntraNotAvailable) {
		t.Errorf("expected ErrEntraNotAvailable on empty scopes, got %v", err)
	}
}

func TestRequestEntraToken_AcquireSilentOnly(t *testing.T) {
	called := false
	source := &fakeEntraSource{
		acquire: func(scopes []string, allowInteractive bool) (string, time.Time, error) {
			called = true
			if allowInteractive {
				t.Errorf("plugin path must call AcquireToken with allowInteractive=false; got true")
			}
			if len(scopes) != 1 || scopes[0] != "User.Read" {
				t.Errorf("scopes round-trip mismatch: %v", scopes)
			}
			return "tok", time.Date(2026, 5, 13, 0, 0, 0, 0, time.UTC), nil
		},
	}
	api := newBoundAPI(t, func() EntraTokenSource { return source })
	token, exp, err := api.RequestEntraToken(context.Background(), []string{"User.Read"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("AcquireToken was not called")
	}
	if token != "tok" {
		t.Errorf("token = %q", token)
	}
	wantExp := time.Date(2026, 5, 13, 0, 0, 0, 0, time.UTC)
	if !exp.Equal(wantExp) {
		t.Errorf("expiry = %v, want %v", exp, wantExp)
	}
}

func TestRequestEntraToken_AcquireErrorMapsToSentinel(t *testing.T) {
	cause := errors.New("interactive required")
	source := &fakeEntraSource{
		acquire: func(_ []string, _ bool) (string, time.Time, error) {
			return "", time.Time{}, cause
		},
	}
	api := newBoundAPI(t, func() EntraTokenSource { return source })
	_, _, err := api.RequestEntraToken(context.Background(), []string{"User.Read"})
	if !errors.Is(err, sdk.ErrEntraNotAvailable) {
		t.Errorf("expected ErrEntraNotAvailable wrapping, got %v", err)
	}
	if !errors.Is(err, cause) {
		t.Errorf("expected underlying cause to be preserved in chain, got %v", err)
	}
}

func TestRequestEntraToken_FreshSourceEachCall(t *testing.T) {
	// Verifies the source callback is invoked on every call — a manager
	// reconfigured mid-session takes effect without a plugin reload.
	calls := 0
	source := func() EntraTokenSource {
		calls++
		return &fakeEntraSource{
			acquire: func(_ []string, _ bool) (string, time.Time, error) {
				return "tok", time.Time{}, nil
			},
		}
	}
	api := newBoundAPI(t, source)
	for i := 0; i < 3; i++ {
		_, _, _ = api.RequestEntraToken(context.Background(), []string{"x"})
	}
	if calls != 3 {
		t.Errorf("entraSource invocations = %d, want 3", calls)
	}
}

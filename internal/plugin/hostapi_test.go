package plugin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
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

// ---------------------------------------------------------------------
// RequestPersonioSession coverage.
// ---------------------------------------------------------------------

// fakePersonioSource is a scripted PersonioSessionSource for hostapi tests.
type fakePersonioSource struct {
	ensure func(ctx context.Context) (PersonioSessionView, error)
}

func (f *fakePersonioSource) EnsureSession(ctx context.Context) (PersonioSessionView, error) {
	if f.ensure == nil {
		return PersonioSessionView{}, errors.New("no script")
	}
	return f.ensure(ctx)
}

func newBoundAPIWithPersonio(t *testing.T, source func() PersonioSessionSource) *boundHostAPI {
	t.Helper()
	return &boundHostAPI{
		pluginName:     "test-plugin",
		log:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		handles:        newHandleRegistry(),
		settings:       newFakeSettings(),
		personioSource: source,
	}
}

func TestRequestPersonioSession_NoSourceReturnsErr(t *testing.T) {
	api := newBoundAPIWithPersonio(t, nil)
	_, err := api.RequestPersonioSession(context.Background())
	if !errors.Is(err, sdk.ErrPersonioNotAvailable) {
		t.Errorf("expected ErrPersonioNotAvailable, got %v", err)
	}
}

func TestRequestPersonioSession_SourceReturnsNil(t *testing.T) {
	api := newBoundAPIWithPersonio(t, func() PersonioSessionSource { return nil })
	_, err := api.RequestPersonioSession(context.Background())
	if !errors.Is(err, sdk.ErrPersonioNotAvailable) {
		t.Errorf("expected ErrPersonioNotAvailable, got %v", err)
	}
}

func TestRequestPersonioSession_HappyPath(t *testing.T) {
	captured := time.Date(2026, 5, 14, 8, 0, 0, 0, time.UTC)
	source := &fakePersonioSource{
		ensure: func(_ context.Context) (PersonioSessionView, error) {
			return PersonioSessionView{
				AppHost:    "example.app.personio.com",
				CSRFToken:  "csrf-abc",
				CapturedAt: captured,
				Cookies: []PersonioCookieView{
					{Name: "PHPSESSID", Value: "abc", Domain: ".personio.de", Path: "/", Secure: true, HTTPOnly: true, SameSite: "lax"},
					{Name: "XSRF-TOKEN", Value: "csrf-abc", Domain: ".personio.de", Path: "/", Secure: true, SameSite: "lax"},
				},
			}, nil
		},
	}
	api := newBoundAPIWithPersonio(t, func() PersonioSessionSource { return source })

	sess, err := api.RequestPersonioSession(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sess.AppHost != "example.app.personio.com" {
		t.Errorf("AppHost = %q", sess.AppHost)
	}
	if sess.CSRFToken != "csrf-abc" {
		t.Errorf("CSRFToken = %q", sess.CSRFToken)
	}
	if !sess.CapturedAt.Equal(captured) {
		t.Errorf("CapturedAt = %v, want %v", sess.CapturedAt, captured)
	}
	if len(sess.Cookies) != 2 {
		t.Fatalf("Cookies len = %d, want 2", len(sess.Cookies))
	}
	if sess.Cookies[0].Name != "PHPSESSID" || !sess.Cookies[0].HTTPOnly {
		t.Errorf("Cookie[0] round-trip mismatch: %+v", sess.Cookies[0])
	}
	if sess.Cookies[1].SameSite != "lax" {
		t.Errorf("Cookie[1].SameSite = %q, want \"lax\"", sess.Cookies[1].SameSite)
	}
}

func TestRequestPersonioSession_SourceErrorWrappedInSentinel(t *testing.T) {
	cause := errors.New("user dismissed login window")
	source := &fakePersonioSource{
		ensure: func(_ context.Context) (PersonioSessionView, error) {
			return PersonioSessionView{}, cause
		},
	}
	api := newBoundAPIWithPersonio(t, func() PersonioSessionSource { return source })
	_, err := api.RequestPersonioSession(context.Background())
	if !errors.Is(err, sdk.ErrPersonioNotAvailable) {
		t.Errorf("expected ErrPersonioNotAvailable wrapping, got %v", err)
	}
	if !errors.Is(err, cause) {
		t.Errorf("expected underlying cause to be preserved in chain, got %v", err)
	}
}

func TestRequestPersonioSession_SourceReturnsSentinelPassThrough(t *testing.T) {
	// When the source itself produces an ErrPersonioNotAvailable
	// (typical path — App's source wraps its own causes), the host
	// must pass it through verbatim instead of double-wrapping.
	source := &fakePersonioSource{
		ensure: func(_ context.Context) (PersonioSessionView, error) {
			return PersonioSessionView{}, fmt.Errorf("%w: no tenant configured", sdk.ErrPersonioNotAvailable)
		},
	}
	api := newBoundAPIWithPersonio(t, func() PersonioSessionSource { return source })
	_, err := api.RequestPersonioSession(context.Background())
	if !errors.Is(err, sdk.ErrPersonioNotAvailable) {
		t.Fatalf("expected ErrPersonioNotAvailable, got %v", err)
	}
	// The original "no tenant configured" detail must survive.
	if !strings.Contains(err.Error(), "no tenant configured") {
		t.Errorf("expected detail to round-trip, got %q", err.Error())
	}
}

func TestRequestPersonioSession_FreshSourceEachCall(t *testing.T) {
	calls := 0
	source := func() PersonioSessionSource {
		calls++
		return &fakePersonioSource{
			ensure: func(_ context.Context) (PersonioSessionView, error) {
				return PersonioSessionView{AppHost: "x"}, nil
			},
		}
	}
	api := newBoundAPIWithPersonio(t, source)
	for i := 0; i < 3; i++ {
		_, _ = api.RequestPersonioSession(context.Background())
	}
	if calls != 3 {
		t.Errorf("personioSource invocations = %d, want 3", calls)
	}
}

// ---------------------------------------------------------------------
// ListTags / PublishTags coverage.
// ---------------------------------------------------------------------

type fakeTagSource struct {
	list func(ctx context.Context) ([]TagView, error)
}

func (f *fakeTagSource) List(ctx context.Context) ([]TagView, error) {
	if f.list == nil {
		return nil, errors.New("no script")
	}
	return f.list(ctx)
}

type fakeTagSink struct {
	publish func(ctx context.Context, plugin string, tags []ImportedTagView) (int, error)
}

func (f *fakeTagSink) Publish(ctx context.Context, plugin string, tags []ImportedTagView) (int, error) {
	if f.publish == nil {
		return 0, errors.New("no script")
	}
	return f.publish(ctx, plugin, tags)
}

func TestListTags_NoSourceReturnsEmpty(t *testing.T) {
	api := &boundHostAPI{
		pluginName: "test",
		log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		handles:    newHandleRegistry(),
	}
	out, err := api.ListTags(context.Background())
	if err != nil {
		t.Fatalf("expected nil err on missing source, got %v", err)
	}
	if len(out) != 0 {
		t.Errorf("expected empty slice, got %d entries", len(out))
	}
}

func TestListTags_ProjectsToHostTag(t *testing.T) {
	src := &fakeTagSource{
		list: func(_ context.Context) ([]TagView, error) {
			return []TagView{
				{ID: 1, Name: "#root"},
				{ID: 2, Name: "#leaf", ParentID: 1, Color: "#7c3aed"},
			}, nil
		},
	}
	api := &boundHostAPI{
		pluginName: "test",
		log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		handles:    newHandleRegistry(),
		tagSource:  func() TagSource { return src },
	}
	out, err := api.ListTags(context.Background())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("got %d tags, want 2", len(out))
	}
	if out[1].ID != 2 || out[1].ParentID != 1 || out[1].Color != "#7c3aed" {
		t.Errorf("projection mismatch on entry 1: %+v", out[1])
	}
}

func TestPublishTags_DeniedWithoutCapability(t *testing.T) {
	called := false
	api := &boundHostAPI{
		pluginName:     "no-cap-plugin",
		log:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		handles:        newHandleRegistry(),
		hasTagProvider: false,
		tagSink: func() TagSink {
			return &fakeTagSink{publish: func(_ context.Context, _ string, _ []ImportedTagView) (int, error) {
				called = true
				return 0, nil
			}}
		},
	}
	_, err := api.PublishTags(context.Background(), []sdk.ImportedTag{{Path: "x"}})
	if !errors.Is(err, sdk.ErrPublishTagsNotAllowed) {
		t.Fatalf("expected ErrPublishTagsNotAllowed, got %v", err)
	}
	if called {
		t.Fatal("sink was invoked despite capability gate")
	}
}

func TestPublishTags_DeniedWithoutSink(t *testing.T) {
	api := &boundHostAPI{
		pluginName:     "ok-cap-plugin",
		log:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		handles:        newHandleRegistry(),
		hasTagProvider: true,
	}
	_, err := api.PublishTags(context.Background(), []sdk.ImportedTag{{Path: "x"}})
	if !errors.Is(err, sdk.ErrPublishTagsNotAllowed) {
		t.Fatalf("expected ErrPublishTagsNotAllowed when host has no sink, got %v", err)
	}
}

func TestPublishTags_HappyPath(t *testing.T) {
	var received []ImportedTagView
	sink := &fakeTagSink{
		publish: func(_ context.Context, plugin string, tags []ImportedTagView) (int, error) {
			received = tags
			if plugin != "tag-plug" {
				t.Errorf("plugin = %q, want tag-plug", plugin)
			}
			return 2, nil
		},
	}
	api := &boundHostAPI{
		pluginName:     "tag-plug",
		log:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		handles:        newHandleRegistry(),
		hasTagProvider: true,
		tagSink:        func() TagSink { return sink },
	}
	in := []sdk.ImportedTag{
		{Path: "a/b", Description: "x"},
		{Path: "c", Color: "#fff"},
	}
	created, err := api.PublishTags(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if created != 2 {
		t.Errorf("created = %d, want 2", created)
	}
	if len(received) != 2 {
		t.Fatalf("sink received %d entries, want 2", len(received))
	}
	if received[0].Path != "a/b" || received[0].Description != "x" {
		t.Errorf("entry 0 mismatch: %+v", received[0])
	}
	if received[1].Color != "#fff" {
		t.Errorf("entry 1 color = %q, want #fff", received[1].Color)
	}
}

func TestPublishTags_EmptyListIsNoop(t *testing.T) {
	sinkCalled := false
	api := &boundHostAPI{
		pluginName:     "tag-plug",
		log:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		handles:        newHandleRegistry(),
		hasTagProvider: true,
		tagSink: func() TagSink {
			return &fakeTagSink{publish: func(_ context.Context, _ string, _ []ImportedTagView) (int, error) {
				sinkCalled = true
				return 0, nil
			}}
		},
	}
	created, err := api.PublishTags(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if created != 0 {
		t.Errorf("created = %d, want 0", created)
	}
	if sinkCalled {
		t.Error("sink should not be called for an empty list")
	}
}

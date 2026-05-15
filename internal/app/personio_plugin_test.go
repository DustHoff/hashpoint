package app

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dusthoff/hashpoint/internal/personio"
)

// stubSessionStore is a minimal in-memory personio.SessionStore replacement
// usable from any OS — the production WinCredSessionStore is Windows-only
// and the MemorySessionStore lives behind a non-Windows build tag.
type stubSessionStore struct {
	mu sync.Mutex
	s  *personio.Session
}

func (m *stubSessionStore) Get() (*personio.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.s == nil {
		return nil, personio.ErrNoSession
	}
	if m.s.Expired() {
		m.s = nil
		return nil, personio.ErrNoSession
	}
	return m.s, nil
}

func (m *stubSessionStore) Set(s *personio.Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.s = s
	return nil
}

func (m *stubSessionStore) Delete() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.s = nil
	return nil
}

func TestTriggerAutoRelogin_CoalescesConcurrentCalls(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	gate := make(chan struct{}) // blocks loginFn until released

	store := &stubSessionStore{}
	src := &personioSessionSource{
		sessions: store,
		logger:   slog.Default(),
		tenant:   func() string { return "example" },
		loginFn: func(ctx context.Context, _ personio.LoginConfig) (*personio.LoginResult, error) {
			calls.Add(1)
			<-gate
			return &personio.LoginResult{Session: &personio.Session{
				Tenant:     "example",
				AppHost:    "example.app.personio.com",
				CapturedAt: time.Now(),
			}}, nil
		},
		validateFn: func(ctx context.Context, _ *personio.Session) error { return nil },
	}

	// First trigger: starts the goroutine. Five repeats while in flight
	// must be absorbed by the CAS guard.
	src.TriggerAutoRelogin()
	for i := 0; i < 5; i++ {
		src.TriggerAutoRelogin()
	}

	// Release and wait for the flag to clear.
	close(gate)
	deadline := time.Now().Add(2 * time.Second)
	for src.autoReloginInFlight.Load() && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if src.autoReloginInFlight.Load() {
		t.Fatal("auto-relogin never completed")
	}

	if got := calls.Load(); got != 1 {
		t.Fatalf("loginFn calls = %d, want 1 (CAS should have absorbed the extras)", got)
	}
}

func TestTriggerAutoRelogin_AllowsFreshTriggerAfterCompletion(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	store := &stubSessionStore{}
	src := &personioSessionSource{
		sessions: store,
		logger:   slog.Default(),
		tenant:   func() string { return "example" },
		loginFn: func(ctx context.Context, _ personio.LoginConfig) (*personio.LoginResult, error) {
			calls.Add(1)
			return &personio.LoginResult{Session: &personio.Session{
				Tenant:     "example",
				AppHost:    "example.app.personio.com",
				CapturedAt: time.Now(),
			}}, nil
		},
		validateFn: func(ctx context.Context, _ *personio.Session) error { return nil },
	}

	waitForIdle := func() {
		deadline := time.Now().Add(2 * time.Second)
		for src.autoReloginInFlight.Load() && time.Now().Before(deadline) {
			time.Sleep(2 * time.Millisecond)
		}
		if src.autoReloginInFlight.Load() {
			t.Fatal("auto-relogin never completed")
		}
	}

	src.TriggerAutoRelogin()
	waitForIdle()
	if got := calls.Load(); got != 1 {
		t.Fatalf("first trigger: loginFn calls = %d, want 1", got)
	}

	// The first login persisted a fresh session — clear it so the next
	// trigger must go through the slow path again (otherwise EnsureSession
	// short-circuits on the fast path and loginFn never runs).
	_ = store.Delete()

	src.TriggerAutoRelogin()
	waitForIdle()
	if got := calls.Load(); got != 2 {
		t.Fatalf("second trigger after Delete: loginFn calls = %d, want 2", got)
	}
}

func TestTriggerAutoRelogin_FastPathWhenSessionFresh(t *testing.T) {
	t.Parallel()
	store := &stubSessionStore{}
	_ = store.Set(&personio.Session{
		Tenant:     "example",
		AppHost:    "example.app.personio.com",
		CapturedAt: time.Now(), // fresh — Expired() returns false
	})
	loginCalled := false
	src := &personioSessionSource{
		sessions: store,
		logger:   slog.Default(),
		tenant:   func() string { return "example" },
		loginFn: func(ctx context.Context, _ personio.LoginConfig) (*personio.LoginResult, error) {
			loginCalled = true
			return nil, errors.New("should not be called")
		},
		validateFn: func(ctx context.Context, _ *personio.Session) error { return nil },
	}

	src.TriggerAutoRelogin()
	deadline := time.Now().Add(1 * time.Second)
	for src.autoReloginInFlight.Load() && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if loginCalled {
		t.Fatal("loginFn was called even though a fresh session existed")
	}
}

// EnsureSession's fast path must NOT hand back cookies the server has
// already rejected — even when the local Session.Expired() heuristic
// still reports false. This regression protects against the production
// bug PR #12 set out to fix: a plugin asking for a session in the
// minute between "Personio invalidated" and "next PersonioCheck tick"
// would otherwise be handed dead cookies.
func TestEnsureSession_FastPathProbesServer(t *testing.T) {
	t.Parallel()
	store := &stubSessionStore{}
	_ = store.Set(&personio.Session{
		Tenant:     "example",
		AppHost:    "example.app.personio.com",
		CapturedAt: time.Now(), // local Expired() = false
	})
	var validateCalls atomic.Int32
	var loginCalls atomic.Int32
	src := &personioSessionSource{
		sessions: store,
		logger:   slog.Default(),
		tenant:   func() string { return "example" },
		loginFn: func(ctx context.Context, _ personio.LoginConfig) (*personio.LoginResult, error) {
			loginCalls.Add(1)
			return &personio.LoginResult{Session: &personio.Session{
				Tenant:     "example",
				AppHost:    "example.app.personio.com",
				CapturedAt: time.Now(),
				Cookies: []personio.SessionCookie{
					{Name: "XSRF-TOKEN", Value: "fresh", Path: "/"},
				},
			}}, nil
		},
		validateFn: func(_ context.Context, _ *personio.Session) error {
			validateCalls.Add(1)
			// First validateFn call (fast-path probe) reports the cookies as
			// dead; the post-login validate (slow-path) passes.
			if validateCalls.Load() == 1 {
				return personio.ErrSessionExpired
			}
			return nil
		},
	}

	if _, err := src.EnsureSession(context.Background()); err != nil {
		t.Fatalf("EnsureSession returned error: %v", err)
	}
	if got := loginCalls.Load(); got != 1 {
		t.Fatalf("expected slow path to run once (loginFn calls = %d, want 1)", got)
	}
	if got := validateCalls.Load(); got < 2 {
		t.Fatalf("expected validateFn to be called for both fast-path probe and post-login probe (got %d, want >=2)", got)
	}
}

// A non-expiry probe failure (5xx, network) must NOT trigger a slow
// path — the cookies might still work, and we don't want a transient
// server hiccup to open Chrome under the user.
func TestEnsureSession_FastPathToleratesTransientProbeError(t *testing.T) {
	t.Parallel()
	store := &stubSessionStore{}
	_ = store.Set(&personio.Session{
		Tenant:     "example",
		AppHost:    "example.app.personio.com",
		CapturedAt: time.Now(),
	})
	var loginCalls atomic.Int32
	src := &personioSessionSource{
		sessions: store,
		logger:   slog.Default(),
		tenant:   func() string { return "example" },
		loginFn: func(ctx context.Context, _ personio.LoginConfig) (*personio.LoginResult, error) {
			loginCalls.Add(1)
			return nil, errors.New("should not be called for transient probe error")
		},
		validateFn: func(_ context.Context, _ *personio.Session) error {
			return errors.New("personio validate: unexpected status 502")
		},
	}

	if _, err := src.EnsureSession(context.Background()); err != nil {
		t.Fatalf("EnsureSession returned error: %v", err)
	}
	if got := loginCalls.Load(); got != 0 {
		t.Fatalf("loginFn was called on transient probe failure (got %d, want 0)", got)
	}
	if _, err := store.Get(); err != nil {
		t.Fatal("transient probe failure should leave the session in the store")
	}
}

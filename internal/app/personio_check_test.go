package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dusthoff/hashpoint/internal/config"
	"github.com/dusthoff/hashpoint/internal/personio"
)

// recordingSessionStore wraps stubSessionStore with a Delete-call counter
// so the purge-on-401 path is verifiable from a test.
type recordingSessionStore struct {
	mu        sync.Mutex
	session   *personio.Session
	deleteN   atomic.Int32
	deleteErr error
}

func (m *recordingSessionStore) Get() (*personio.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.session == nil {
		return nil, personio.ErrNoSession
	}
	if m.session.Expired() {
		m.session = nil
		return nil, personio.ErrNoSession
	}
	return m.session, nil
}

func (m *recordingSessionStore) Set(s *personio.Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.session = s
	return nil
}

func (m *recordingSessionStore) Delete() error {
	m.deleteN.Add(1)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.deleteErr != nil {
		return m.deleteErr
	}
	m.session = nil
	return nil
}

// freshSession returns a Session whose CapturedAt is "now" so the
// 24h Expired() heuristic returns false — i.e. the fast-path-would-
// match condition the production bug surfaced under.
func freshSession() *personio.Session {
	return &personio.Session{
		Tenant:     "lmis",
		AppHost:    "lmis.app.personio.com",
		CapturedAt: time.Now().UTC(),
		Cookies: []personio.SessionCookie{
			{Name: "XSRF-TOKEN", Value: "x", Path: "/"},
		},
	}
}

func newAppForCheckTest(store *recordingSessionStore, validate func(ctx context.Context, sess *personio.Session) error) *App {
	cfg := config.Default()
	cfg.Personio.Tenant = "lmis"
	return &App{
		ctx:    context.Background(),
		logger: slog.Default(),
		cfg:    cfg,
		deps: Deps{
			Sessions: store,
		},
		validatePersonio: validate,
	}
}

func TestPersonioCheck_PurgesSessionOnErrSessionExpired(t *testing.T) {
	t.Parallel()
	store := &recordingSessionStore{}
	if err := store.Set(freshSession()); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	validate := func(_ context.Context, _ *personio.Session) error {
		return fmt.Errorf("personio validate: unauthenticated (status 401): %w", personio.ErrSessionExpired)
	}
	app := newAppForCheckTest(store, validate)

	st := app.PersonioCheck()
	if st.Valid {
		t.Fatal("expected st.Valid = false on ErrSessionExpired")
	}
	if got := store.deleteN.Load(); got != 1 {
		t.Fatalf("Sessions.Delete calls = %d, want 1", got)
	}
	if _, err := store.Get(); err == nil {
		t.Fatal("expected store to be empty after purge, but Get returned a session")
	}
}

func TestPersonioCheck_KeepsSessionOnNonExpiryError(t *testing.T) {
	t.Parallel()
	store := &recordingSessionStore{}
	if err := store.Set(freshSession()); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	// 5xx / unexpected-status: server is sick, cookies might still be
	// valid. Must NOT purge.
	validate := func(_ context.Context, _ *personio.Session) error {
		return errors.New("personio validate: unexpected status 502")
	}
	app := newAppForCheckTest(store, validate)

	st := app.PersonioCheck()
	if st.Valid {
		t.Fatal("expected st.Valid = false on probe failure")
	}
	if got := store.deleteN.Load(); got != 0 {
		t.Fatalf("Sessions.Delete calls = %d, want 0 (non-expiry error must not purge)", got)
	}
	if _, err := store.Get(); err != nil {
		t.Fatal("expected session to remain in store after non-expiry probe failure")
	}
}

func TestPersonioCheck_NoPurgeOnSuccess(t *testing.T) {
	t.Parallel()
	store := &recordingSessionStore{}
	if err := store.Set(freshSession()); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	validate := func(_ context.Context, _ *personio.Session) error { return nil }
	app := newAppForCheckTest(store, validate)

	st := app.PersonioCheck()
	if !st.Valid {
		t.Fatalf("expected st.Valid = true on successful probe; got reason=%q", st.Reason)
	}
	if got := store.deleteN.Load(); got != 0 {
		t.Fatalf("Sessions.Delete calls = %d, want 0", got)
	}
}

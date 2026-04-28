//go:build !windows

package personio

import (
	"errors"
	"testing"
	"time"
)

func TestMemorySessionStore_DropsExpiredSession(t *testing.T) {
	t.Parallel()
	store := NewMemorySessionStore()
	aged := &Session{
		Tenant:     "example",
		CapturedAt: time.Now().Add(-MaxSessionAge - time.Minute).UTC(),
	}
	if err := store.Set(aged); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := store.Get()
	if !errors.Is(err, ErrNoSession) {
		t.Fatalf("Get err=%v want ErrNoSession", err)
	}
	if got != nil {
		t.Fatalf("Get returned non-nil session for expired entry: %+v", got)
	}
	// A second Get must also report missing — the purge has to be sticky.
	if _, err := store.Get(); !errors.Is(err, ErrNoSession) {
		t.Fatalf("second Get err=%v want ErrNoSession", err)
	}
}

func TestMemorySessionStore_KeepsFreshSession(t *testing.T) {
	t.Parallel()
	store := NewMemorySessionStore()
	fresh := &Session{
		Tenant:     "example",
		CapturedAt: time.Now().UTC(),
	}
	if err := store.Set(fresh); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := store.Get()
	if err != nil {
		t.Fatalf("Get err=%v want nil", err)
	}
	if got != fresh {
		t.Fatalf("Get returned %+v want %+v", got, fresh)
	}
}

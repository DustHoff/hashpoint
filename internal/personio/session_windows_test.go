//go:build windows

package personio

import (
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/danieljoos/wincred"
)

// uniqueTarget returns a wincred target name that won't collide with the
// real session entry or with parallel test runs.
func uniqueTarget(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("TimeTracker.PersonioSession.test.%d.%d", os.Getpid(), time.Now().UnixNano())
}

func writeTestSession(t *testing.T, target string, sess *Session) {
	t.Helper()
	blob, err := MarshalSession(sess)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	c := wincred.NewGenericCredential(target)
	c.CredentialBlob = blob
	c.UserName = "TimeTracker-test"
	if err := c.Write(); err != nil {
		t.Skipf("wincred unavailable on this host: %v", err)
	}
	t.Cleanup(func() {
		if got, err := wincred.GetGenericCredential(target); err == nil {
			_ = got.Delete()
		}
	})
}

func TestWinCredSessionStore_PurgesExpiredSession(t *testing.T) {
	target := uniqueTarget(t)
	store := &WinCredSessionStore{Target: target}

	aged := &Session{
		Tenant:     "smoke",
		CapturedAt: time.Now().Add(-MaxSessionAge - time.Minute).UTC(),
	}
	writeTestSession(t, target, aged)

	got, err := store.Get()
	if !errors.Is(err, ErrNoSession) {
		t.Fatalf("Get err=%v want ErrNoSession", err)
	}
	if got != nil {
		t.Fatalf("Get returned non-nil session for expired entry: %+v", got)
	}

	// Aged entry must be gone from the Credential Manager — defense against
	// the cookie sitting in storage past its useful life.
	if _, err := wincred.GetGenericCredential(target); err == nil {
		t.Fatalf("expected credential entry to be purged, but it still exists")
	}
}

func TestWinCredSessionStore_KeepsFreshSession(t *testing.T) {
	target := uniqueTarget(t)
	store := &WinCredSessionStore{Target: target}

	fresh := &Session{
		Tenant:     "smoke",
		AppHost:    "smoke.app.personio.com",
		EmployeeID: 4711,
		CapturedAt: time.Now().UTC(),
		Cookies: []SessionCookie{
			{Name: "XSRF-TOKEN", Value: "abc%3D"},
		},
	}
	if err := store.Set(fresh); err != nil {
		t.Skipf("wincred unavailable on this host: %v", err)
	}
	t.Cleanup(func() { _ = store.Delete() })

	got, err := store.Get()
	if err != nil {
		t.Fatalf("Get err=%v want nil", err)
	}
	if got == nil {
		t.Fatalf("Get returned nil session")
	}
	if got.Tenant != fresh.Tenant || got.EmployeeID != fresh.EmployeeID || got.AppHost != fresh.AppHost {
		t.Fatalf("session round-trip mismatch: got %+v want %+v", got, fresh)
	}
	if got.XSRFToken() != "abc=" {
		t.Fatalf("XSRFToken=%q want %q", got.XSRFToken(), "abc=")
	}
}

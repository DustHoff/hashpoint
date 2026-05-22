package app

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/dusthoff/hashpoint/internal/entra"
)

// fakeEntraManager is a scripted entra.Manager for EntraProbe tests. It records
// how AcquireToken was called so the test can assert the probe never requests
// an interactive token and skips the network entirely when no account exists.
type fakeEntraManager struct {
	configured     bool
	status         entra.Status
	acquireErr     error
	acquireCalls   int
	gotInteractive bool
}

func (f *fakeEntraManager) Configured() bool                      { return f.configured }
func (f *fakeEntraManager) Status(context.Context) entra.Status   { return f.status }
func (f *fakeEntraManager) Login(context.Context, []string) error { return nil }
func (f *fakeEntraManager) Logout(context.Context) error          { return nil }

func (f *fakeEntraManager) AcquireToken(_ context.Context, _ []string, allowInteractive bool) (string, time.Time, error) {
	f.acquireCalls++
	if allowInteractive {
		f.gotInteractive = true
	}
	if f.acquireErr != nil {
		return "", time.Time{}, f.acquireErr
	}
	return "access-token", time.Now().Add(time.Hour), nil
}

func newAppForEntraProbe(mgr entra.Manager) *App {
	a := &App{ctx: context.Background(), logger: slog.Default()}
	a.entraMgr = mgr
	return a
}

func TestEntraProbe_NotConfigured(t *testing.T) {
	t.Parallel()
	// A nil manager means the feature is dormant: no panic, configured=false,
	// and a reason the badge can render. This mirrors the unconfigured branch
	// of EntraStatus so the header badge can hide itself.
	resp := newAppForEntraProbe(nil).EntraProbe()
	if resp.Configured {
		t.Error("Configured = true, want false for a nil manager")
	}
	if resp.Valid {
		t.Error("Valid = true, want false for a nil manager")
	}
	if resp.Reason == "" {
		t.Error("expected a non-empty reason for the dormant feature")
	}
}

func TestEntraProbe(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		status      entra.Status
		acquireErr  error
		wantValid   bool
		wantReason  string
		wantAcquire int
	}{
		{
			name:        "configured but no account",
			status:      entra.Status{},
			wantValid:   false,
			wantReason:  "nicht angemeldet",
			wantAcquire: 0, // must not probe the token endpoint without an account
		},
		{
			name:        "account with usable token",
			status:      entra.Status{HasAccount: true, Username: "u@example.com", TenantID: "t"},
			wantValid:   true,
			wantReason:  "",
			wantAcquire: 1,
		},
		{
			name:        "account but interactive required",
			status:      entra.Status{HasAccount: true},
			acquireErr:  entra.ErrInteractiveRequired,
			wantValid:   false,
			wantReason:  "Anmeldung erforderlich",
			wantAcquire: 1,
		},
		{
			name:        "account but signed out",
			status:      entra.Status{HasAccount: true},
			acquireErr:  entra.ErrSignedOut,
			wantValid:   false,
			wantReason:  "Anmeldung erforderlich",
			wantAcquire: 1,
		},
		{
			name:        "account but transient failure",
			status:      entra.Status{HasAccount: true},
			acquireErr:  errors.New("network is unreachable"),
			wantValid:   false,
			wantReason:  "Token-Prüfung fehlgeschlagen",
			wantAcquire: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			mgr := &fakeEntraManager{configured: true, status: tt.status, acquireErr: tt.acquireErr}
			resp := newAppForEntraProbe(mgr).EntraProbe()

			if !resp.Configured {
				t.Error("Configured = false, want true")
			}
			if resp.HasAccount != tt.status.HasAccount {
				t.Errorf("HasAccount = %v, want %v", resp.HasAccount, tt.status.HasAccount)
			}
			if resp.Valid != tt.wantValid {
				t.Errorf("Valid = %v, want %v", resp.Valid, tt.wantValid)
			}
			if resp.Reason != tt.wantReason {
				t.Errorf("Reason = %q, want %q", resp.Reason, tt.wantReason)
			}
			if mgr.acquireCalls != tt.wantAcquire {
				t.Errorf("AcquireToken calls = %d, want %d", mgr.acquireCalls, tt.wantAcquire)
			}
			if mgr.gotInteractive {
				t.Error("EntraProbe must never request an interactive token")
			}
			if tt.wantValid && resp.CheckedAt == "" {
				t.Error("expected CheckedAt to be set on a successful probe")
			}
		})
	}
}

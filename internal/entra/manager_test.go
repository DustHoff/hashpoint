package entra

import (
	"strings"
	"testing"
)

func TestNewManager_RejectsEmptyConfig(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		opts   Options
		errMsg string
	}{
		{
			"missing client_id",
			Options{TenantID: "11111111-2222-3333-4444-555555555555", CacheDir: t.TempDir()},
			"client_id",
		},
		{
			"missing tenant_id",
			Options{ClientID: "abc", CacheDir: t.TempDir()},
			"tenant_id",
		},
		{
			"missing cache dir",
			Options{ClientID: "abc", TenantID: "def"},
			"cache dir",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewManager(tc.opts)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.errMsg) {
				t.Errorf("error %q does not mention %q", err, tc.errMsg)
			}
		})
	}
}

func TestDefaultLoginScopes_ExcludesMSALReservedScopes(t *testing.T) {
	t.Parallel()
	// MSAL-go injects openid/profile/offline_access itself via
	// AppendDefaultScopes AND strictly compares the user-supplied
	// scope list against the token response. Entra never echoes
	// offline_access in the response scope claim (it materialises as
	// a separate refresh_token field instead), so including any of
	// these in DefaultLoginScopes makes every interactive login fail
	// with "declined scopes are present: offline_access". This test
	// locks the contract so a well-meaning maintainer cannot
	// re-introduce the regression.
	reserved := map[string]struct{}{
		"openid":         {},
		"profile":        {},
		"offline_access": {},
	}
	for _, s := range DefaultLoginScopes {
		if _, bad := reserved[s]; bad {
			t.Errorf("DefaultLoginScopes contains reserved scope %q — MSAL-go injects it itself; including it triggers the declined-scopes failure", s)
		}
	}
}

func TestNewManager_BuildsClientWithGoodInput(t *testing.T) {
	// Smoke test that public.New accepts our authority/clientID combo
	// without trying to hit the network. We don't run interactive flows
	// here — that requires a real browser + Entra ID.
	t.Parallel()
	dir := t.TempDir()
	mgr, err := NewManager(Options{
		ClientID: "11111111-2222-3333-4444-555555555555",
		TenantID: "22222222-3333-4444-5555-666666666666",
		CacheDir: dir,
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if !mgr.Configured() {
		t.Fatal("Configured() should be true after successful NewManager")
	}
}

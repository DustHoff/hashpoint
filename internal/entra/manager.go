// Package entra wires the optional Microsoft Entra ID authentication into
// the TimeTracker. The package owns the MSAL public-client app, the
// DPAPI-encrypted token cache and the small surface the rest of the app
// uses to obtain access tokens for Microsoft Graph (SharePoint, calendar)
// and Entra-protected custom APIs.
//
// Authentication is *additive*: the rest of the app must run unchanged
// when the user has not configured Entra ID. main.go therefore constructs
// a Manager only when config.Entra.Configured() is true; otherwise the
// Wails bindings short-circuit to "feature off" and no MSAL code paths
// are exercised.
//
// Tokens, refresh tokens and account records never leave the package as
// plaintext: the on-disk cache is DPAPI-encrypted (CurrentUser scope) and
// the in-memory copy lives only inside the MSAL Client. We deliberately
// never log access tokens, refresh tokens, ID-token claims or the user's
// UPN at INFO level.
package entra

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/public"
)

// DefaultLoginScopes is the bare-minimum delegated scope set we
// request on first login. User.Read pulls the signed-in user's profile
// so the status badge can show the username. Resource-specific scopes
// (Sites.Read.All, Calendars.Read, …) are requested incrementally by
// per-resource clients via AcquireToken.
//
// Reserved scopes (openid, profile, offline_access) MUST NOT appear
// here even though we depend on them: MSAL-go injects them itself
// via AppendDefaultScopes before the wire request, AND it strictly
// compares the user-supplied scope list against the token response's
// echoed scope claim. Entra never echoes offline_access (it materialises
// as a separate refresh_token field instead), so including it here
// makes MSAL-go flag the login as "declined scopes are present:
// offline_access" — see findDeclinedScopes in MSAL-go accesstokens.
// The fix is purely client-side; the wire payload is identical.
var DefaultLoginScopes = []string{
	"https://graph.microsoft.com/User.Read",
}

// Timeouts are generous on the interactive path (the user might need to
// type an MFA code) and tight on the silent path (an unreachable MSAL
// endpoint shouldn't block the UI more than a few seconds).
const (
	interactiveLoginTimeout = 5 * time.Minute
	silentTokenTimeout      = 30 * time.Second
)

// Status describes the currently signed-in account from the persistent
// cache's perspective. Used by the Wails-bound EntraStatus method to
// drive the Settings-tab badge.
type Status struct {
	HasAccount    bool   `json:"has_account"`
	Username      string `json:"username,omitempty"`
	HomeAccountID string `json:"home_account_id,omitempty"`
	TenantID      string `json:"tenant_id,omitempty"`
	ClientID      string `json:"client_id,omitempty"`
}

// Options bundles construction parameters for NewManager.
type Options struct {
	// ClientID is the Application (client) ID GUID from the Entra ID
	// App Registration.
	ClientID string
	// TenantID is the Directory (tenant) ID GUID. Single-tenant only —
	// "common"/"organizations" are rejected upstream by config.Validate.
	TenantID string
	// CacheDir is the directory the encrypted MSAL cache lives in,
	// typically %LOCALAPPDATA%\TimeTracker\auth.
	CacheDir string
	// Logger is optional; defaults to slog.Default().
	Logger *slog.Logger
	// RedirectURI overrides the OAuth loopback target. Default
	// "http://localhost" (any free port). Override only when a fixed
	// port is required by firewall policy and matches a port-bearing
	// redirect URI in the App Registration.
	RedirectURI string
}

// Manager is the small interface the rest of the app sees. It hides the
// MSAL client and on-disk cache plumbing.
type Manager interface {
	// Configured returns true once a usable client_id/tenant_id pair has
	// been wired in. Always true for a Manager returned by NewManager —
	// this is on the interface so bindings can be type-checked against
	// nil-Manager guards.
	Configured() bool
	// Status reports whether a cached account is present and, if so, who.
	// Never hits the network.
	Status(ctx context.Context) Status
	// Login runs AcquireTokenInteractive with the system browser and a
	// loopback redirect. Promptless on Entra-joined devices via PRT-SSO.
	Login(ctx context.Context, scopes []string) error
	// Logout removes every cached account and deletes the on-disk cache
	// file. The OS session is unaffected.
	Logout(ctx context.Context) error
	// AcquireToken returns a Bearer-suitable access token and its expiry
	// (UTC) for the given scopes. Tries the cache first; falls back to
	// interactive only when allowInteractive is set AND the silent path
	// failed. Mixing scopes across resources (e.g. Graph + custom API)
	// in one call is rejected by Entra ID — issue one AcquireToken per
	// resource.
	AcquireToken(ctx context.Context, scopes []string, allowInteractive bool) (string, time.Time, error)
}

type manager struct {
	opts        Options
	client      public.Client
	cache       *fileCache
	logger      *slog.Logger
	redirectURI string
}

// NewManager builds a Manager wired against the configured tenant, with
// a DPAPI-encrypted file cache rooted at opts.CacheDir.
func NewManager(opts Options) (Manager, error) {
	if strings.TrimSpace(opts.ClientID) == "" || strings.TrimSpace(opts.TenantID) == "" {
		return nil, ErrNotConfigured
	}
	if opts.CacheDir == "" {
		return nil, errors.New("entra: cache dir must be set")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.RedirectURI == "" {
		opts.RedirectURI = "http://localhost"
	}

	cachePath := filepath.Join(opts.CacheDir, "msal_cache.bin")
	fc := newFileCache(cachePath, newDPAPICipher(), opts.Logger)

	authority := "https://login.microsoftonline.com/" + opts.TenantID
	client, err := public.New(opts.ClientID,
		public.WithAuthority(authority),
		public.WithCache(fc),
	)
	if err != nil {
		return nil, fmt.Errorf("create MSAL client: %w", err)
	}
	return &manager{
		opts:        opts,
		client:      client,
		cache:       fc,
		logger:      opts.Logger,
		redirectURI: opts.RedirectURI,
	}, nil
}

// Configured implements Manager.Configured.
func (m *manager) Configured() bool { return true }

// Status implements Manager.Status.
func (m *manager) Status(ctx context.Context) Status {
	base := Status{ClientID: m.opts.ClientID, TenantID: m.opts.TenantID}
	accs, err := m.client.Accounts(ctx)
	if err != nil || len(accs) == 0 {
		return base
	}
	a := accs[0]
	base.HasAccount = true
	base.Username = a.PreferredUsername
	base.HomeAccountID = a.HomeAccountID
	return base
}

// Login implements Manager.Login.
func (m *manager) Login(ctx context.Context, scopes []string) error {
	if len(scopes) == 0 {
		scopes = DefaultLoginScopes
	}
	loginCtx, cancel := context.WithTimeout(ctx, interactiveLoginTimeout)
	defer cancel()
	res, err := m.client.AcquireTokenInteractive(loginCtx, scopes,
		public.WithRedirectURI(m.redirectURI),
	)
	if err != nil {
		return fmt.Errorf("interactive login: %w", err)
	}
	// We deliberately log neither the access token nor the UPN. The
	// "expires_in_sec" field is benign and helps debugging short-lived
	// CA-policy enforcement windows.
	m.logger.Info("entra: interactive login completed",
		"expires_in_sec", int(time.Until(res.ExpiresOn).Seconds()),
	)
	return nil
}

// Logout implements Manager.Logout.
func (m *manager) Logout(ctx context.Context) error {
	accs, err := m.client.Accounts(ctx)
	if err != nil {
		return fmt.Errorf("list accounts: %w", err)
	}
	for _, a := range accs {
		if err := m.client.RemoveAccount(ctx, a); err != nil {
			return fmt.Errorf("remove account: %w", err)
		}
	}
	if err := m.cache.Clear(); err != nil {
		return fmt.Errorf("clear cache: %w", err)
	}
	m.logger.Info("entra: logged out (cache cleared)")
	return nil
}

// AcquireToken implements Manager.AcquireToken.
func (m *manager) AcquireToken(ctx context.Context, scopes []string, allowInteractive bool) (string, time.Time, error) {
	if len(scopes) == 0 {
		return "", time.Time{}, errors.New("entra: scopes required")
	}

	silentCtx, silentCancel := context.WithTimeout(ctx, silentTokenTimeout)
	defer silentCancel()

	accs, err := m.client.Accounts(silentCtx)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("list accounts: %w", err)
	}
	if len(accs) == 0 {
		if !allowInteractive {
			return "", time.Time{}, ErrSignedOut
		}
		return m.acquireInteractive(ctx, scopes)
	}

	res, err := m.client.AcquireTokenSilent(silentCtx, scopes,
		public.WithSilentAccount(accs[0]),
	)
	if err == nil {
		return res.AccessToken, res.ExpiresOn.UTC(), nil
	}

	if !allowInteractive {
		m.logger.Info("entra: silent acquisition failed — caller did not allow interactive",
			"err", err)
		return "", time.Time{}, fmt.Errorf("%w: %w", ErrInteractiveRequired, err)
	}
	m.logger.Info("entra: silent acquisition failed — falling back to interactive",
		"err", err)
	return m.acquireInteractive(ctx, scopes)
}

func (m *manager) acquireInteractive(ctx context.Context, scopes []string) (string, time.Time, error) {
	interCtx, cancel := context.WithTimeout(ctx, interactiveLoginTimeout)
	defer cancel()
	res, err := m.client.AcquireTokenInteractive(interCtx, scopes,
		public.WithRedirectURI(m.redirectURI),
	)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("interactive token: %w", err)
	}
	return res.AccessToken, res.ExpiresOn.UTC(), nil
}

package plugin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/dusthoff/hashpoint/plugin/sdk"
)

// EntraTokenSource is the narrow surface boundHostAPI needs to serve
// sdk.HostAPI.RequestEntraToken. Declared locally so internal/plugin
// stays free of an internal/entra import (and tests can stub the source
// without spinning up MSAL). entra.Manager satisfies this interface
// directly via duck typing.
type EntraTokenSource interface {
	AcquireToken(ctx context.Context, scopes []string, allowInteractive bool) (string, time.Time, error)
}

// PersonioSessionView is the host-side payload PersonioSessionSource
// returns. Mirrors sdk.PersonioSession's shape (with a SDK-free cookie
// type) so the App layer can populate it without importing the SDK.
type PersonioSessionView struct {
	AppHost    string
	CSRFToken  string
	Cookies    []PersonioCookieView
	CapturedAt time.Time
}

// PersonioCookieView mirrors sdk.PersonioCookie one-for-one.
type PersonioCookieView struct {
	Name     string
	Value    string
	Domain   string
	Path     string
	Expires  time.Time
	Secure   bool
	HTTPOnly bool
	SameSite string
}

// PersonioSessionSource is the narrow surface boundHostAPI needs to
// serve sdk.HostAPI.RequestPersonioSession. The implementation lives in
// internal/app and owns the stored session, re-authentication flow
// (interactive CDP login), and concurrency control — boundHostAPI is
// just a pass-through. Declared locally so internal/plugin stays free
// of an internal/personio import and tests can stub the source.
type PersonioSessionSource interface {
	// EnsureSession returns a session that is "usable now": the
	// implementation has already validated freshness and, if necessary,
	// driven an interactive re-authentication. Returns an error wrapping
	// sdk.ErrPersonioNotAvailable for a user-abort or any other
	// unrecoverable situation; boundHostAPI passes the error through
	// unchanged so plugins can errors.Is against the sentinel.
	EnsureSession(ctx context.Context) (PersonioSessionView, error)
}

// boundHostAPI is the host-side sdk.HostAPI implementation handed to a
// single plugin via Plugin.Init. It is "bound" to the plugin's name so
// secret redemption refuses handles minted for a different plugin —
// defensive layer in case a leaked handle is replayed by a malicious or
// buggy plugin.
type boundHostAPI struct {
	pluginName string
	log        *slog.Logger
	handles    *handleRegistry
	settings   SettingsStore
	// entraSource returns the current Entra ID manager, or nil when
	// the feature is not configured. Invoked on every
	// RequestEntraToken call so a freshly-configured manager takes
	// effect for running plugins without a reload.
	entraSource func() EntraTokenSource
	// personioSource returns the current Personio session source, or
	// nil when no tenant is configured / the host is not wired for
	// Personio. Invoked on every RequestPersonioSession call so a
	// freshly-configured tenant takes effect without a plugin reload.
	personioSource func() PersonioSessionSource
}

// RedeemSecret resolves the handle to (plugin, key), confirms the plugin
// name matches the caller, and returns the plaintext from the settings
// store. Returns ErrUnknownSecretHandle for stale, cross-plugin, or
// since-deleted secrets — callers should treat that as a non-retryable
// configuration error.
func (a *boundHostAPI) RedeemSecret(ctx context.Context, h sdk.SecretHandle) (string, error) {
	entry, ok := a.handles.lookup(h)
	if !ok {
		return "", fmt.Errorf("%w: stale handle", sdk.ErrUnknownSecretHandle)
	}
	if entry.pluginName != a.pluginName {
		// Either a bug (we shipped the wrong handle) or a malicious
		// replay. Log the mismatch but do not reveal the other plugin's
		// name to the caller.
		a.log.Warn("plugin: cross-plugin secret redeem refused",
			"caller", a.pluginName, "handle_owner", entry.pluginName)
		return "", fmt.Errorf("%w: not owned by caller", sdk.ErrUnknownSecretHandle)
	}
	v, found, err := a.settings.GetSecret(ctx, a.pluginName, entry.secretKey)
	if err != nil {
		return "", fmt.Errorf("redeem %s/%s: %w", a.pluginName, entry.secretKey, err)
	}
	if !found {
		return "", fmt.Errorf("%w: secret no longer present", sdk.ErrUnknownSecretHandle)
	}
	return v, nil
}

// RequestEntraToken serves the plugin a Bearer-suitable Entra ID
// access token + expiry, scoped to the slice the plugin supplied. The
// host always runs MSAL silently (allowInteractive=false); plugins
// cannot pop a browser window mid-session. Any failure — feature
// dormant, signed out, refresh-token expired, scopes need consent —
// collapses to sdk.ErrEntraNotAvailable so the plugin can branch on a
// single sentinel.
//
// The refresh token never leaves the host's MSAL cache (DPAPI-encrypted
// at rest, CurrentUser scope), by design: a compromised plugin can mint
// only access tokens for the duration of the host process.
func (a *boundHostAPI) RequestEntraToken(ctx context.Context, scopes []string) (string, time.Time, error) {
	if a.entraSource == nil {
		return "", time.Time{}, fmt.Errorf("%w: plugin host not wired for entra", sdk.ErrEntraNotAvailable)
	}
	mgr := a.entraSource()
	if mgr == nil {
		return "", time.Time{}, fmt.Errorf("%w: entra not configured", sdk.ErrEntraNotAvailable)
	}
	if len(scopes) == 0 {
		return "", time.Time{}, fmt.Errorf("%w: scopes required", sdk.ErrEntraNotAvailable)
	}
	token, expiresAt, err := mgr.AcquireToken(ctx, scopes, false)
	if err != nil {
		// Log the underlying cause at Debug — this runs on the
		// plugin's cadence and could be noisy. Tokens / scopes are
		// not in the error string by MSAL convention, but be careful
		// not to widen the log surface here.
		a.log.Debug("plugin entra token: acquisition failed",
			"plugin", a.pluginName, "err", err)
		return "", time.Time{}, fmt.Errorf("%w: %w", sdk.ErrEntraNotAvailable, err)
	}
	return token, expiresAt, nil
}

// RequestPersonioSession serves the plugin a usable Personio session:
// AppHost, URL-decoded CSRF token, and the captured cookies. Unlike
// Entra there is no silent refresh path — when the stored session is
// missing / stale the source drives an interactive Chrome login (CDP)
// which may take minutes; the source owns the mutex that prevents
// parallel windows when several plugins ask at once. Any failure —
// no tenant, source not wired, user aborted the login, validation
// rejected the renewed session — collapses to sdk.ErrPersonioNotAvailable
// so the plugin can branch on a single sentinel.
//
// Cookies are the full session secret: the host MUST NOT log them and
// trusts that the SDK contract has warned plugin authors not to either.
func (a *boundHostAPI) RequestPersonioSession(ctx context.Context) (sdk.PersonioSession, error) {
	if a.personioSource == nil {
		return sdk.PersonioSession{}, fmt.Errorf("%w: plugin host not wired for personio", sdk.ErrPersonioNotAvailable)
	}
	src := a.personioSource()
	if src == nil {
		return sdk.PersonioSession{}, fmt.Errorf("%w: personio not configured", sdk.ErrPersonioNotAvailable)
	}
	view, err := src.EnsureSession(ctx)
	if err != nil {
		// Source returned the wrapped sentinel itself (typical path) or
		// some other error we still classify as "not available". Log at
		// Debug to keep the noise floor low; cookies / CSRF must not
		// appear here — we only echo the source's err which is allowed
		// to mention reasons ("tenant unset", "user aborted login").
		a.log.Debug("plugin personio session: ensure failed",
			"plugin", a.pluginName, "err", err)
		if errors.Is(err, sdk.ErrPersonioNotAvailable) {
			return sdk.PersonioSession{}, err
		}
		return sdk.PersonioSession{}, fmt.Errorf("%w: %w", sdk.ErrPersonioNotAvailable, err)
	}
	out := sdk.PersonioSession{
		AppHost:    view.AppHost,
		CSRFToken:  view.CSRFToken,
		CapturedAt: view.CapturedAt,
	}
	if len(view.Cookies) > 0 {
		out.Cookies = make([]sdk.PersonioCookie, 0, len(view.Cookies))
		for _, c := range view.Cookies {
			out.Cookies = append(out.Cookies, sdk.PersonioCookie{
				Name:     c.Name,
				Value:    c.Value,
				Domain:   c.Domain,
				Path:     c.Path,
				Expires:  c.Expires,
				Secure:   c.Secure,
				HTTPOnly: c.HTTPOnly,
				SameSite: c.SameSite,
			})
		}
	}
	return out, nil
}

// Log forwards a structured log line to the host's slog with an attached
// "plugin" attribute. Unknown levels degrade to Info. The plugin's name
// is filled in by the host — plugins must not echo it in fields.
func (a *boundHostAPI) Log(_ context.Context, level, message string, fields map[string]string) error {
	attrs := make([]any, 0, 2*len(fields))
	for k, v := range fields {
		// Refuse fields that would override the host-injected plugin name.
		if k == "plugin" {
			continue
		}
		attrs = append(attrs, k, v)
	}
	switch level {
	case "debug":
		a.log.Debug(message, attrs...)
	case "warn":
		a.log.Warn(message, attrs...)
	case "error":
		a.log.Error(message, attrs...)
	default:
		a.log.Info(message, attrs...)
	}
	return nil
}

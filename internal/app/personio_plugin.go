package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/dusthoff/hashpoint/internal/config"
	"github.com/dusthoff/hashpoint/internal/personio"
	pluginhost "github.com/dusthoff/hashpoint/internal/plugin"
	"github.com/dusthoff/hashpoint/plugin/sdk"
)

// personioReauthTimeout caps an interactive CDP login triggered by a
// plugin's RequestPersonioSession call. Mirrors personio.Login's own
// default but is set explicitly here so a frozen Chrome cannot keep a
// plugin's RPC call blocked indefinitely.
const personioReauthTimeout = 5 * time.Minute

// personioSessionSource implements pluginhost.PersonioSessionSource on
// top of the App's Personio dependencies (session store, login flow,
// current tenant). One instance is created per App and shared across
// plugins — the embedded mutex serialises concurrent EnsureSession
// callers so only one Chrome window opens at a time during a stale-
// session re-authentication.
type personioSessionSource struct {
	sessions personio.SessionStore
	logger   *slog.Logger
	// tenant returns the currently-configured Personio tenant slug.
	// Re-evaluated on every call so a SaveConfig that changes the
	// tenant takes effect without a plugin reload.
	tenant func() string
	// loginFn drives the interactive CDP login. Defaults to
	// personio.Login; overridable in tests to avoid spinning up Chrome.
	loginFn func(ctx context.Context, cfg personio.LoginConfig) (*personio.LoginResult, error)
	// validateFn validates a freshly-captured session. Defaults to
	// personio.Validate; overridable in tests.
	validateFn func(ctx context.Context, sess *personio.Session) error

	mu sync.Mutex
}

// newPersonioSessionSource constructs the source with production-default
// loginFn / validateFn. tenantFn must not be nil — it is the only way
// the source can learn which tenant to drive Chrome against.
func newPersonioSessionSource(sessions personio.SessionStore, tenantFn func() string, logger *slog.Logger) *personioSessionSource {
	if logger == nil {
		logger = slog.Default()
	}
	return &personioSessionSource{
		sessions:   sessions,
		logger:     logger,
		tenant:     tenantFn,
		loginFn:    personio.Login,
		validateFn: personio.Validate,
	}
}

// EnsureSession returns a session that is "usable now". Fast path
// (session exists and is within MaxSessionAge) returns immediately.
// Stale or absent session triggers a CDP login, validates it, and
// persists the result before returning. Concurrent callers see the
// fresh session because the mutex spans the whole flow — only one
// Chrome window opens at a time.
func (p *personioSessionSource) EnsureSession(ctx context.Context) (pluginhost.PersonioSessionView, error) {
	if p == nil || p.sessions == nil || p.tenant == nil {
		return pluginhost.PersonioSessionView{}, fmt.Errorf("%w: source not configured", sdk.ErrPersonioNotAvailable)
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	// Fast path: stored session is still fresh enough.
	if sess, err := p.sessions.Get(); err == nil && sess != nil && !sess.Expired() {
		return personioViewFrom(sess), nil
	}

	tenant := strings.TrimSpace(p.tenant())
	if tenant == "" {
		return pluginhost.PersonioSessionView{}, fmt.Errorf("%w: no tenant configured", sdk.ErrPersonioNotAvailable)
	}

	loginCtx, cancel := context.WithTimeout(ctx, personioReauthTimeout)
	defer cancel()

	p.logger.Info("personio session source: stored session missing/stale — launching CDP login",
		"tenant", tenant)
	res, err := p.loginFn(loginCtx, personio.LoginConfig{
		Tenant:  tenant,
		Logger:  p.logger,
		Timeout: personioReauthTimeout,
	})
	if err != nil {
		return pluginhost.PersonioSessionView{}, fmt.Errorf("%w: login: %w", sdk.ErrPersonioNotAvailable, err)
	}
	if res == nil || res.Session == nil {
		return pluginhost.PersonioSessionView{}, fmt.Errorf("%w: login returned empty session", sdk.ErrPersonioNotAvailable)
	}
	if err := p.validateFn(loginCtx, res.Session); err != nil {
		return pluginhost.PersonioSessionView{}, fmt.Errorf("%w: validation: %w", sdk.ErrPersonioNotAvailable, err)
	}
	// Best-effort: resolve employee id so the session blob is complete
	// when other call paths (Sync, manual UI use) reuse it. Failure is
	// non-fatal — the rest of Hashpoint can resolve this on demand.
	if cli, err := personio.NewUIClient(personio.UIClientOptions{Session: res.Session, Logger: p.logger}); err == nil {
		if eid, err := cli.FetchEmployeeID(loginCtx); err == nil && eid != 0 {
			res.Session.EmployeeID = eid
		} else if err != nil {
			p.logger.Warn("personio session source: could not pre-resolve employee id", "err", err)
		}
	}
	if err := p.sessions.Set(res.Session); err != nil {
		return pluginhost.PersonioSessionView{}, fmt.Errorf("%w: persist session: %w", sdk.ErrPersonioNotAvailable, err)
	}
	p.logger.Info("personio session source: reauth complete",
		"tenant", res.Session.Tenant,
		"app_host", res.Session.AppHost,
		"employee_id", res.Session.EmployeeID)
	return personioViewFrom(res.Session), nil
}

// personioViewFrom projects a personio.Session onto the host-side
// PersonioSessionView the plugin bridge consumes. The XSRF cookie is
// already URL-decoded by Session.XSRFToken().
func personioViewFrom(sess *personio.Session) pluginhost.PersonioSessionView {
	out := pluginhost.PersonioSessionView{
		AppHost:    sess.AppHost,
		CSRFToken:  sess.XSRFToken(),
		CapturedAt: sess.CapturedAt,
	}
	if len(sess.Cookies) > 0 {
		out.Cookies = make([]pluginhost.PersonioCookieView, 0, len(sess.Cookies))
		for _, c := range sess.Cookies {
			out.Cookies = append(out.Cookies, pluginhost.PersonioCookieView{
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
	return out
}

// currentPersonioSessionSource is the PersonioSource lambda the plugin
// host holds. Returns nil when no tenant is configured or the App was
// constructed without Personio wiring — the bound HostAPI surfaces
// sdk.ErrPersonioNotAvailable in that case.
func (a *App) currentPersonioSessionSource() pluginhost.PersonioSessionSource {
	if a.personioSrc == nil {
		return nil
	}
	cfg := a.GetConfig()
	if cfg == nil || strings.TrimSpace(cfg.Personio.Tenant) == "" {
		return nil
	}
	return a.personioSrc
}

// currentPersonioTenant is the tenant-resolver passed to
// newPersonioSessionSource. Pulled out so the source picks up
// SaveConfig changes without a plugin reload.
func (a *App) currentPersonioTenant() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cfg == nil {
		return ""
	}
	return config.NormalizeTenant(a.cfg.Personio.Tenant)
}

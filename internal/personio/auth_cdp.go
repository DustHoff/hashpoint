package personio

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// LoginConfig configures the interactive Personio login flow.
type LoginConfig struct {
	// Tenant is the Personio subdomain (without "personio.de").
	Tenant string
	// Logger receives lifecycle events. Defaults to slog.Default().
	Logger *slog.Logger
	// Timeout for the entire interactive session (browser open until login).
	// Defaults to 5 minutes.
	Timeout time.Duration
	// Headless drives Chrome without UI. Useful for tests; defaults to false
	// so the user can see and interact with the login page.
	Headless bool
}

// LoginResult holds everything the caller needs after a successful login.
type LoginResult struct {
	Session *Session
}

// Login launches a Chrome instance pointing at the configured Personio
// tenant's login page, waits until the user has signed in (i.e. navigation
// has moved away from /login and the session cookie is set), captures all
// cookies for the personio.de domain, then closes Chrome.
//
// The returned session has Tenant and Cookies populated. EmployeeID is left
// at zero — call Validate or fetch /api/v1/navigation/context to fill it.
func Login(ctx context.Context, cfg LoginConfig) (*LoginResult, error) {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	tenant := strings.TrimSpace(cfg.Tenant)
	if tenant == "" {
		return nil, errors.New("personio login: tenant must be set")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Minute
	}

	// Personio splits its real estate across personio.de (marketing /
	// per-tenant landing pages) and app.personio.com (the app shell). The
	// .de subdomain has reliably worked as the entry point for the login
	// flow and ends up redirecting to the per-tenant <tenant>.app.personio.com
	// after authentication; we capture the actual landing host below.
	loginURL := "https://" + tenant + ".personio.de/login/index"

	allocOpts := append(
		chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", cfg.Headless),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
	)

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, allocOpts...)
	defer cancelAlloc()

	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer cancelBrowser()

	timeoutCtx, cancelTimeout := context.WithTimeout(browserCtx, cfg.Timeout)
	defer cancelTimeout()

	cfg.Logger.Info("personio login: launching browser", "tenant", tenant, "url", loginURL)

	if err := chromedp.Run(timeoutCtx,
		network.Enable(),
		chromedp.Navigate(loginURL),
	); err != nil {
		return nil, fmt.Errorf("launch browser: %w", err)
	}

	appHost, err := waitForAuthenticated(timeoutCtx, tenant, cfg.Logger)
	if err != nil {
		return nil, err
	}

	cookies, err := chromedpCookies(timeoutCtx)
	if err != nil {
		return nil, fmt.Errorf("read cookies: %w", err)
	}
	cfg.Logger.Info("personio login: captured cookies", "count", len(cookies), "app_host", appHost)

	sess := &Session{
		Tenant:     tenant,
		AppHost:    appHost,
		Cookies:    filterPersonioCookies(cookies),
		CapturedAt: time.Now().UTC(),
	}
	if sess.XSRFToken() == "" {
		return nil, errors.New("personio login: no XSRF/CSRF cookie captured — login may have failed")
	}

	// Closing the browser context closes the Chrome window.
	cancelTimeout()
	cancelBrowser()

	return &LoginResult{Session: sess}, nil
}

// waitForAuthenticated polls the current URL until the user has completed
// the login flow. We accept "authenticated" when the browser is on either a
// .personio.de or .app.personio.com hostname AND not on a /login path. The
// returned host is the Personio app host the browser ended up on (e.g.
// "lmis.app.personio.com") — Personio splits per-tenant SPAs onto a
// dedicated subdomain that we then use as the API base.
func waitForAuthenticated(ctx context.Context, tenant string, logger *slog.Logger) (string, error) {
	deadline := time.Now().Add(5 * time.Minute)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}

	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-tick.C:
		}

		var current string
		if err := chromedp.Run(ctx, chromedp.Location(&current)); err != nil {
			if time.Now().After(deadline) {
				return "", fmt.Errorf("location poll: %w", err)
			}
			continue
		}
		u, err := url.Parse(current)
		if err != nil {
			continue
		}
		host := u.Hostname()
		if !isPersonioHost(host) {
			continue
		}
		if strings.HasPrefix(u.Path, "/login") || strings.Contains(u.Path, "/auth") {
			continue
		}
		// Give the page a beat so any post-redirect cookies are set.
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(750 * time.Millisecond):
		}
		// Re-read the location once more; the SPA may navigate again
		// after the initial redirect.
		var settled string
		_ = chromedp.Run(ctx, chromedp.Location(&settled))
		if settled != "" {
			if su, err := url.Parse(settled); err == nil && isPersonioHost(su.Hostname()) {
				host = su.Hostname()
				current = settled
			}
		}
		// Prefer an "app.personio.com" host because that is where the UI
		// API lives. If the browser is still on the marketing subdomain
		// keep waiting.
		if !strings.HasSuffix(host, ".app.personio.com") &&
			!strings.HasSuffix(host, ".app.personio.de") {
			// Personio sometimes lands on <tenant>.personio.de/dashboard
			// before the SPA redirects to <tenant>.app.personio.com. Keep
			// polling unless we're past the deadline.
			if time.Now().Before(deadline) {
				continue
			}
		}
		logger.Info("personio login: detected authenticated navigation", "url", current)
		return host, nil
	}
}

func isPersonioHost(h string) bool {
	h = strings.ToLower(h)
	return strings.HasSuffix(h, ".personio.de") ||
		strings.HasSuffix(h, ".personio.com") ||
		h == "personio.de" || h == "personio.com"
}

func chromedpCookies(ctx context.Context) ([]*network.Cookie, error) {
	var cookies []*network.Cookie
	err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		got, err := network.GetCookies().Do(ctx)
		if err != nil {
			return err
		}
		cookies = got
		return nil
	}))
	return cookies, err
}

func filterPersonioCookies(in []*network.Cookie) []SessionCookie {
	out := make([]SessionCookie, 0, len(in))
	for _, c := range in {
		d := strings.ToLower(c.Domain)
		if !strings.HasSuffix(d, ".personio.de") &&
			!strings.HasSuffix(d, ".personio.com") &&
			d != "personio.de" && d != "personio.com" {
			continue
		}
		sc := SessionCookie{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   c.Domain,
			Path:     c.Path,
			Secure:   c.Secure,
			HTTPOnly: c.HTTPOnly,
			SameSite: string(c.SameSite),
		}
		// chromedp expresses Expires as seconds since epoch (float).
		if c.Expires > 0 {
			sec := int64(c.Expires)
			nsec := int64((c.Expires - float64(sec)) * float64(time.Second))
			sc.Expires = time.Unix(sec, nsec).UTC()
		}
		out = append(out, sc)
	}
	return out
}

// Validate sends a GET request to the Personio app root and returns nil if
// the response stays inside the authenticated app (i.e. is not a redirect to
// /login). It uses the cookies in the supplied session — so call this after
// a Login() to confirm the captured cookies actually work.
func Validate(ctx context.Context, sess *Session) error {
	if sess == nil {
		return ErrNoSession
	}
	host := strings.TrimSpace(sess.AppHost)
	if host == "" {
		// Fall back to the marketing host while the session is being
		// freshly captured; AppHost may not be populated yet.
		if t := strings.TrimSpace(sess.Tenant); t != "" {
			host = t + ".personio.de"
		} else {
			return errors.New("personio validate: session has no host")
		}
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return err
	}
	appURL, err := url.Parse("https://" + host + "/")
	if err != nil {
		return err
	}
	jar.SetCookies(appURL, sess.HTTPCookies())

	// Disable automatic redirect following so we can inspect the first hop.
	cli := &http.Client{
		Jar:     jar,
		Timeout: 10 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, appURL.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := cli.Do(req)
	if err != nil {
		return fmt.Errorf("validate: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		loc := resp.Header.Get("Location")
		if loc != "" {
			lu, _ := url.Parse(loc)
			if lu != nil && strings.HasPrefix(lu.Path, "/login") {
				return errors.New("personio validate: redirected to /login — session expired")
			}
		}
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("personio validate: unauthenticated (status %d)", resp.StatusCode)
	}
	return nil
}

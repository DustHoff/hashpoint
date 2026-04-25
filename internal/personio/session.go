// Package personio implements the Personio integration that backs the
// TimeTracker sync feature.
//
// Authentication uses the same cookies the user obtains by logging into the
// Personio web UI. The interactive login happens out-of-process: we drive a
// real Chrome instance via the Chrome DevTools Protocol (see auth_cdp.go),
// then capture the resulting session cookies and store them in the Windows
// Credential Manager. All API calls then ride on top of those cookies and
// echo the XSRF-TOKEN value back as the X-CSRF-Token header — the same
// pattern the Personio JS frontend uses.
package personio

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func urlPathUnescape(s string) (string, error) { return url.QueryUnescape(s) }

// Session captures everything we need to talk to the Personio UI API on
// behalf of an interactively-authenticated user.
type Session struct {
	// Tenant is the Personio subdomain slug (e.g. "example"). Used as the
	// entry point for the interactive login.
	Tenant string `json:"tenant"`
	// AppHost is the actual host the user landed on after authentication
	// (e.g. "example.app.personio.com"). All UI-API calls are dispatched
	// against this host — Personio splits the marketing/login domain
	// (personio.de / app.personio.com) and the per-tenant app shell.
	AppHost string `json:"app_host"`
	// EmployeeID is the numeric employee identifier returned from
	// /api/v1/navigation/context.
	EmployeeID int64 `json:"employee_id"`
	// Cookies are the cookies as captured at the end of an interactive login.
	// Only fields the standard library cookie jar needs to round-trip the
	// values are kept (Name, Value, Domain, Path, Expires, Secure, HTTPOnly,
	// SameSite). Other CDP fields are dropped.
	Cookies []SessionCookie `json:"cookies"`
	// CapturedAt records when the session was harvested. Used to surface
	// "session is N hours old" hints in the UI.
	CapturedAt time.Time `json:"captured_at"`
}

// SessionCookie is the JSON-serialisable subset of an http.Cookie kept in the
// credential store.
type SessionCookie struct {
	Name     string    `json:"name"`
	Value    string    `json:"value"`
	Domain   string    `json:"domain"`
	Path     string    `json:"path"`
	Expires  time.Time `json:"expires,omitempty"`
	Secure   bool      `json:"secure"`
	HTTPOnly bool      `json:"http_only"`
	SameSite string    `json:"same_site,omitempty"`
}

// HTTPCookies converts the stored cookies to the *http.Cookie form expected
// by Go's cookie jar.
func (s Session) HTTPCookies() []*http.Cookie {
	out := make([]*http.Cookie, 0, len(s.Cookies))
	for _, c := range s.Cookies {
		hc := &http.Cookie{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   c.Domain,
			Path:     c.Path,
			Secure:   c.Secure,
			HttpOnly: c.HTTPOnly,
		}
		if !c.Expires.IsZero() {
			hc.Expires = c.Expires
		}
		switch strings.ToLower(c.SameSite) {
		case "lax":
			hc.SameSite = http.SameSiteLaxMode
		case "strict":
			hc.SameSite = http.SameSiteStrictMode
		case "none":
			hc.SameSite = http.SameSiteNoneMode
		}
		out = append(out, hc)
	}
	return out
}

// XSRFToken returns the value to be echoed via the x-athena-xsrf-token
// header. Personio uses a Laravel-style URL-encoded cookie called
// "XSRF-TOKEN"; we URL-decode it before returning. As a defensive fallback
// any cookie whose name contains "xsrf" or "csrf" is considered. Returns
// "" if no such cookie is present.
func (s Session) XSRFToken() string {
	pick := func(name string) (string, bool) {
		for _, c := range s.Cookies {
			if strings.EqualFold(c.Name, name) {
				if dec, err := urlDecode(c.Value); err == nil {
					return dec, true
				}
				return c.Value, true
			}
		}
		return "", false
	}
	if v, ok := pick("XSRF-TOKEN"); ok {
		return v
	}
	for _, c := range s.Cookies {
		l := strings.ToLower(c.Name)
		if strings.Contains(l, "xsrf") || strings.Contains(l, "csrf") {
			if dec, err := urlDecode(c.Value); err == nil {
				return dec
			}
			return c.Value
		}
	}
	return ""
}

// urlDecode is a tiny wrapper to keep the imports tidy.
func urlDecode(s string) (string, error) {
	// net/url is imported by the package elsewhere — we vendor it here too.
	return urlPathUnescape(s)
}

// ErrNoSession is returned when no session has been captured yet (or it has
// been deleted).
var ErrNoSession = errors.New("personio: no session stored")

// SessionStore abstracts session persistence. The Windows implementation in
// session_windows.go uses the Windows Credential Manager.
type SessionStore interface {
	Get() (*Session, error)
	Set(s *Session) error
	Delete() error
}

// SessionCredentialTarget is the wincred entry name used for the captured
// Personio session blob.
const SessionCredentialTarget = "TimeTracker.PersonioSession"

// MarshalSession encodes a session for credential-manager storage.
func MarshalSession(s *Session) ([]byte, error) { return json.Marshal(s) }

// UnmarshalSession decodes a credential-manager blob back into a Session.
func UnmarshalSession(b []byte) (*Session, error) {
	var s Session
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

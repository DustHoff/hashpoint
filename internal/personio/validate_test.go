package personio

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeSession returns a Session with a single XSRF cookie so XSRFToken()
// finds something to send. Domain is left empty so the jar attaches the
// cookie against whatever host the test server runs on.
func fakeSession() *Session {
	return &Session{
		Tenant:     "lmis",
		AppHost:    "lmis.app.personio.com",
		CapturedAt: time.Now().UTC(),
		Cookies: []SessionCookie{
			{Name: "XSRF-TOKEN", Value: "deadbeef", Path: "/"},
			{Name: "PERSONIO_SESSION", Value: "session-value", Path: "/"},
		},
	}
}

func TestValidateAt_Success200(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/navigation/context" {
			t.Errorf("unexpected probe path %q", r.URL.Path)
		}
		if got := r.Header.Get("x-athena-xsrf-token"); got != "deadbeef" {
			t.Errorf("missing XSRF header: got %q", got)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true,"data":{}}`))
	}))
	defer srv.Close()

	if err := validateAt(context.Background(), &http.Client{Timeout: 5 * time.Second}, srv.URL, fakeSession()); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
}

func TestValidateAt_401IsExpired(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	err := validateAt(context.Background(), &http.Client{Timeout: 5 * time.Second}, srv.URL, fakeSession())
	if err == nil {
		t.Fatal("expected error for 401")
	}
	if !strings.Contains(err.Error(), "unauthenticated") {
		t.Errorf("expected unauthenticated error, got %v", err)
	}
}

func TestValidateAt_403IsExpired(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	err := validateAt(context.Background(), &http.Client{Timeout: 5 * time.Second}, srv.URL, fakeSession())
	if err == nil {
		t.Fatal("expected error for 403")
	}
}

func TestValidateAt_LoginRedirectIsExpired(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "/login/index")
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()

	err := validateAt(context.Background(), &http.Client{Timeout: 5 * time.Second}, srv.URL, fakeSession())
	if err == nil {
		t.Fatal("expected error for /login redirect")
	}
	if !strings.Contains(err.Error(), "/login") {
		t.Errorf("expected /login marker in error, got %v", err)
	}
}

func TestValidateAt_AuthRedirectIsExpired(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "/auth/sign-in")
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()

	err := validateAt(context.Background(), &http.Client{Timeout: 5 * time.Second}, srv.URL, fakeSession())
	if err == nil {
		t.Fatal("expected error for /auth redirect")
	}
}

// Personio's app shell used to return a non-/login redirect (e.g. to the
// app subdomain) for half-valid cookies; the bug was that the old Validate
// treated this as success. The probe now hits the API and any redirect is
// treated as failure.
func TestValidateAt_OtherRedirectFailsClosed(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "https://lmis.app.personio.com/dashboard")
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()

	err := validateAt(context.Background(), &http.Client{Timeout: 5 * time.Second}, srv.URL, fakeSession())
	if err == nil {
		t.Fatal("expected error for non-/login redirect")
	}
	if !strings.Contains(err.Error(), "unexpected redirect") {
		t.Errorf("expected unexpected-redirect error, got %v", err)
	}
}

func TestValidateAt_5xxIsError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	err := validateAt(context.Background(), &http.Client{Timeout: 5 * time.Second}, srv.URL, fakeSession())
	if err == nil {
		t.Fatal("expected error for 502")
	}
	if !strings.Contains(err.Error(), "unexpected status") {
		t.Errorf("expected unexpected-status error, got %v", err)
	}
}

func TestValidate_NilSession(t *testing.T) {
	t.Parallel()
	err := Validate(context.Background(), nil)
	if !errors.Is(err, ErrNoSession) {
		t.Fatalf("expected ErrNoSession, got %v", err)
	}
}

func TestValidate_NoHostNoTenant(t *testing.T) {
	t.Parallel()
	err := Validate(context.Background(), &Session{})
	if err == nil || !strings.Contains(err.Error(), "no host") {
		t.Fatalf("expected no-host error, got %v", err)
	}
}

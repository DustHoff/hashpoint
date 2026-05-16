package feedback

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeGitHub stands in for github.com + api.github.com. Tests instal
// route handlers via mux; the helper buildClient wires the resulting
// httptest.Server URLs into a fresh Client with an in-memory store.
type fakeGitHub struct {
	mu        sync.Mutex
	device    *httptest.Server
	api       *httptest.Server
	deviceMux *http.ServeMux
	apiMux    *http.ServeMux
}

func newFakeGitHub() *fakeGitHub {
	g := &fakeGitHub{
		deviceMux: http.NewServeMux(),
		apiMux:    http.NewServeMux(),
	}
	g.device = httptest.NewServer(g.deviceMux)
	g.api = httptest.NewServer(g.apiMux)
	return g
}

func (g *fakeGitHub) Close() {
	g.device.Close()
	g.api.Close()
}

func (g *fakeGitHub) handleAuth(path string, h http.HandlerFunc) {
	g.deviceMux.HandleFunc(path, h)
}

func (g *fakeGitHub) handleAPI(path string, h http.HandlerFunc) {
	g.apiMux.HandleFunc(path, h)
}

func buildClient(t *testing.T, g *fakeGitHub, store TokenStore, now func() time.Time) *Client {
	t.Helper()
	return NewClient(Options{
		AuthBaseURL: g.device.URL,
		APIBaseURL:  g.api.URL,
		ClientID:    "test-client",
		Owner:       "octo",
		Repo:        "demo",
		Store:       store,
		Now:         now,
	})
}

func TestStartDeviceLogin_ParsesResponse(t *testing.T) {
	g := newFakeGitHub()
	defer g.Close()
	g.handleAuth("/login/device/code", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("client_id") != "test-client" {
			t.Errorf("client_id=%q want test-client", r.Form.Get("client_id"))
		}
		if r.Form.Has("scope") {
			t.Errorf("scope must not be sent for GitHub App device flow, got %q", r.Form.Get("scope"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_code":      "deviceXYZ",
			"user_code":        "WDJB-MJHT",
			"verification_uri": "https://github.com/login/device",
			"expires_in":       900,
			"interval":         5,
		})
	})
	frozen := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	c := buildClient(t, g, NewMemoryTokenStore(), func() time.Time { return frozen })
	dc, err := c.StartDeviceLogin(context.Background())
	if err != nil {
		t.Fatalf("StartDeviceLogin: %v", err)
	}
	if dc.DeviceCode != "deviceXYZ" || dc.UserCode != "WDJB-MJHT" || dc.Interval != 5 {
		t.Errorf("unexpected device code: %+v", dc)
	}
	if !dc.ExpiresAt.Equal(frozen.Add(900 * time.Second)) {
		t.Errorf("ExpiresAt=%v want %v", dc.ExpiresAt, frozen.Add(900*time.Second))
	}
}

func TestPollDeviceLogin_SuccessPersistsAndFetchesLogin(t *testing.T) {
	g := newFakeGitHub()
	defer g.Close()
	g.handleAuth("/login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != grantDeviceCode {
			t.Errorf("grant_type=%q want %q", r.Form.Get("grant_type"), grantDeviceCode)
		}
		if r.Form.Get("device_code") != "deviceXYZ" {
			t.Errorf("device_code mismatch: %q", r.Form.Get("device_code"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":             "u2s-token",
			"token_type":               "bearer",
			"expires_in":               28800,
			"refresh_token":            "refresh-XYZ",
			"refresh_token_expires_in": 15897600,
		})
	})
	g.handleAPI("/user", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer u2s-token" {
			t.Errorf("Authorization=%q want Bearer u2s-token", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"login": "octocat"})
	})
	store := NewMemoryTokenStore()
	frozen := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	c := buildClient(t, g, store, func() time.Time { return frozen })

	res, err := c.PollDeviceLogin(context.Background(), "deviceXYZ")
	if err != nil {
		t.Fatalf("PollDeviceLogin: %v", err)
	}
	if res.Status != PollStatusLinked {
		t.Fatalf("status=%q want linked", res.Status)
	}
	got, err := store.Get()
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if got.AccessToken != "u2s-token" || got.RefreshToken != "refresh-XYZ" {
		t.Errorf("token mismatch: %+v", got)
	}
	if got.Login != "octocat" {
		t.Errorf("Login=%q want octocat", got.Login)
	}
	if !got.ExpiresAt.Equal(frozen.Add(28800 * time.Second)) {
		t.Errorf("ExpiresAt=%v want %v", got.ExpiresAt, frozen.Add(28800*time.Second))
	}
}

func TestPollDeviceLogin_ErrorBranches(t *testing.T) {
	cases := []struct {
		name string
		body map[string]any
		want PollStatus
	}{
		{"pending", map[string]any{"error": "authorization_pending"}, PollStatusPending},
		{"slow", map[string]any{"error": "slow_down", "interval": 10}, PollStatusSlowDown},
		{"expired", map[string]any{"error": "expired_token"}, PollStatusExpired},
		{"denied", map[string]any{"error": "access_denied"}, PollStatusDenied},
		{"other", map[string]any{"error": "unknown_error"}, PollStatusError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newFakeGitHub()
			defer g.Close()
			g.handleAuth("/login/oauth/access_token", func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(tc.body)
			})
			c := buildClient(t, g, NewMemoryTokenStore(), nil)
			res, err := c.PollDeviceLogin(context.Background(), "any")
			if err != nil {
				t.Fatalf("PollDeviceLogin: %v", err)
			}
			if res.Status != tc.want {
				t.Errorf("status=%q want %q", res.Status, tc.want)
			}
			if tc.name == "slow" && res.Interval != 10 {
				t.Errorf("slow_down interval=%d want 10", res.Interval)
			}
		})
	}
}

func TestEnsureToken_RefreshesExpired(t *testing.T) {
	g := newFakeGitHub()
	defer g.Close()
	store := NewMemoryTokenStore()
	frozen := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	_ = store.Set(&Token{
		AccessToken:      "old",
		RefreshToken:     "rfsh",
		ExpiresAt:        frozen.Add(-1 * time.Minute), // expired
		RefreshExpiresAt: frozen.Add(30 * 24 * time.Hour),
		IssuedAt:         frozen.Add(-9 * time.Hour),
		Login:            "octocat",
	})
	var hits int32
	g.handleAuth("/login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != grantRefreshToken {
			t.Errorf("grant_type=%q want %q", r.Form.Get("grant_type"), grantRefreshToken)
		}
		if r.Form.Get("refresh_token") != "rfsh" {
			t.Errorf("refresh_token=%q want rfsh", r.Form.Get("refresh_token"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":             "new",
			"refresh_token":            "rfsh2",
			"expires_in":               28800,
			"refresh_token_expires_in": 15897600,
		})
	})
	c := buildClient(t, g, store, func() time.Time { return frozen })

	tok, err := c.EnsureToken(context.Background())
	if err != nil {
		t.Fatalf("EnsureToken: %v", err)
	}
	if tok != "new" {
		t.Errorf("tok=%q want new", tok)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Errorf("refresh endpoint hits=%d want 1", hits)
	}
	stored, _ := store.Get()
	if stored.AccessToken != "new" || stored.RefreshToken != "rfsh2" {
		t.Errorf("token not persisted: %+v", stored)
	}
	if stored.Login != "octocat" {
		t.Errorf("Login lost across refresh: %q", stored.Login)
	}
}

func TestEnsureToken_ReauthWhenRefreshExpired(t *testing.T) {
	store := NewMemoryTokenStore()
	frozen := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	_ = store.Set(&Token{
		AccessToken:      "old",
		RefreshToken:     "rfsh",
		ExpiresAt:        frozen.Add(-1 * time.Minute),
		RefreshExpiresAt: frozen.Add(-1 * time.Minute),
	})
	g := newFakeGitHub()
	defer g.Close()
	c := buildClient(t, g, store, func() time.Time { return frozen })
	if _, err := c.EnsureToken(context.Background()); !errors.Is(err, ErrReauthRequired) {
		t.Fatalf("err=%v want ErrReauthRequired", err)
	}
}

func TestEnsureToken_NotLinkedWhenStoreEmpty(t *testing.T) {
	g := newFakeGitHub()
	defer g.Close()
	c := buildClient(t, g, NewMemoryTokenStore(), nil)
	if _, err := c.EnsureToken(context.Background()); !errors.Is(err, ErrNotLinked) {
		t.Fatalf("err=%v want ErrNotLinked", err)
	}
}

func TestEnsureLabels_CreatesMissingSkipsExisting(t *testing.T) {
	g := newFakeGitHub()
	defer g.Close()
	store := withFreshToken(t)
	created := make(map[string]bool)
	var createMu sync.Mutex
	g.handleAPI("/repos/octo/demo/labels/", func(w http.ResponseWriter, r *http.Request) {
		// GET /labels/{name} — existing for "bug", missing for "severity:critical".
		if r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/repos/octo/demo/labels/")
		if name == "bug" {
			_, _ = w.Write([]byte(`{"name":"bug"}`))
			return
		}
		http.NotFound(w, r)
	})
	g.handleAPI("/repos/octo/demo/labels", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		createMu.Lock()
		created[body["name"]] = true
		createMu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"name":"`+body["name"]+`"}`)
	})
	c := buildClient(t, g, store, nil)
	if err := c.EnsureLabels(context.Background(), []string{"bug", "severity:critical", "user-feedback"}); err != nil {
		t.Fatalf("EnsureLabels: %v", err)
	}
	if created["bug"] {
		t.Errorf("existing label should not have been re-created")
	}
	if !created["severity:critical"] || !created["user-feedback"] {
		t.Errorf("missing labels not created: %v", created)
	}
}

func TestCreateIssue_PostsAndDecodes(t *testing.T) {
	g := newFakeGitHub()
	defer g.Close()
	store := withFreshToken(t)
	g.handleAPI("/repos/octo/demo/issues", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer fresh" {
			t.Errorf("Authorization=%q want Bearer fresh", got)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["title"] != "hello" {
			t.Errorf("title=%v want hello", body["title"])
		}
		labels, _ := body["labels"].([]any)
		if len(labels) != 2 || labels[0] != "bug" || labels[1] != "user-feedback" {
			t.Errorf("labels=%v", labels)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"html_url": "https://github.com/octo/demo/issues/42",
			"number":   42,
		})
	})
	c := buildClient(t, g, store, nil)
	out, err := c.CreateIssue(context.Background(), "hello", "world", []string{"bug", "user-feedback"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if out.Number != 42 || out.HTMLURL != "https://github.com/octo/demo/issues/42" {
		t.Errorf("issue=%+v", out)
	}
}

// withFreshToken returns a store seeded with a never-expiring access
// token so EnsureToken short-circuits without a refresh.
func withFreshToken(t *testing.T) TokenStore {
	t.Helper()
	store := NewMemoryTokenStore()
	if err := store.Set(&Token{AccessToken: "fresh"}); err != nil {
		t.Fatalf("seed store: %v", err)
	}
	return store
}

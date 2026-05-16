package feedback

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Default endpoint roots. Overridable via NewClient options so tests can
// point at an httptest.Server.
const (
	defaultAuthBaseURL = "https://github.com"
	defaultAPIBaseURL  = "https://api.github.com"
)

// Device Flow grant types per RFC 8628 + GitHub conventions.
const (
	grantDeviceCode   = "urn:ietf:params:oauth:grant-type:device_code"
	grantRefreshToken = "refresh_token"
)

// PollStatus is the discrete outcome of a single Device-Flow poll. The
// UI consumes this to decide whether to keep polling, surface an error,
// or transition to the connected state.
type PollStatus string

// Poll outcomes. The trailing comment is the authoritative description
// (revive's exported rule requires a block-level annotation as well).
const (
	PollStatusPending  PollStatus = "pending"   // user has not authorised yet — keep polling
	PollStatusLinked   PollStatus = "linked"    // token persisted, ready to use
	PollStatusSlowDown PollStatus = "slow_down" // GitHub asks us to back off; widened interval is in the response
	PollStatusExpired  PollStatus = "expired"   // device_code timed out — restart the flow
	PollStatusDenied   PollStatus = "denied"    // user clicked "Cancel" on the device page
	PollStatusError    PollStatus = "error"     // anything else (network, 5xx, malformed payload)
)

// ErrReauthRequired is returned by EnsureToken when both the access
// token has expired and the refresh token is no longer valid. Callers
// must trigger a full Device-Flow re-authorisation.
var ErrReauthRequired = errors.New("feedback: re-authorisation required")

// ErrNotLinked is returned by methods that need an access token when no
// token has ever been stored (or it was deleted).
var ErrNotLinked = errors.New("feedback: not linked to GitHub")

// DeviceCode is the public face of a Device-Flow handshake. The
// device_code field stays in the backend; the user only ever sees
// UserCode + VerificationURI.
type DeviceCode struct {
	DeviceCode      string    `json:"-"`
	UserCode        string    `json:"user_code"`
	VerificationURI string    `json:"verification_uri"`
	Interval        int       `json:"interval"`
	ExpiresAt       time.Time `json:"expires_at"`
}

// PollResult carries the outcome of a single poll plus, for slow_down,
// the new interval GitHub asked for.
type PollResult struct {
	Status   PollStatus `json:"status"`
	Interval int        `json:"interval,omitempty"`
	Error    string     `json:"error,omitempty"`
}

// IssueCreated is the response payload of CreateIssue.
type IssueCreated struct {
	HTMLURL string `json:"html_url"`
	Number  int    `json:"number"`
}

// LabelSpec defines a single label the app auto-creates if it is
// missing in the target repo. The colour is a GitHub hex string
// (no leading '#').
type LabelSpec struct {
	Name        string
	Color       string
	Description string
}

// DefaultLabels enumerates every label the feedback flow may attach to
// an issue. The bug/enhancement/question triplet ships with every new
// GitHub repo by default; severity:* and user-feedback are custom and
// will be created on first submit if missing.
var DefaultLabels = []LabelSpec{
	{Name: "bug", Color: "d73a4a", Description: "Something isn't working"},
	{Name: "enhancement", Color: "a2eeef", Description: "New feature or request"},
	{Name: "question", Color: "d876e3", Description: "Further information is requested"},
	{Name: "severity:low", Color: "c2c2c2", Description: "Low severity — cosmetic or convenience"},
	{Name: "severity:medium", Color: "fbca04", Description: "Medium severity — workaround exists"},
	{Name: "severity:high", Color: "f97316", Description: "High severity — important feature broken"},
	{Name: "severity:critical", Color: "b60205", Description: "Critical — data loss, crash, or blocker"},
	{Name: "user-feedback", Color: "0e8a16", Description: "Submitted via the in-app feedback tab"},
}

// Options bundles the wiring for NewClient. AuthBaseURL / APIBaseURL
// default to the github.com endpoints when empty; tests pass an
// httptest.Server URL.
type Options struct {
	HTTPClient  *http.Client
	AuthBaseURL string
	APIBaseURL  string
	ClientID    string
	Owner       string
	Repo        string
	Store       TokenStore
	Logger      *slog.Logger
	// Now lets tests freeze the clock for refresh-window assertions.
	// Defaults to time.Now when nil.
	Now func() time.Time
}

// Client talks to GitHub on behalf of the feedback flow. Methods are
// safe for concurrent use because the token store is the only mutable
// state and the wincred-backed implementation serialises its writes.
type Client struct {
	http        *http.Client
	authBaseURL string
	apiBaseURL  string
	clientID    string
	owner       string
	repo        string
	store       TokenStore
	logger      *slog.Logger
	now         func() time.Time
}

// NewClient constructs the client with sensible defaults. A nil
// TokenStore is accepted (the methods that need it surface
// ErrNotLinked); useful for the StartDeviceLogin-only paths in tests.
func NewClient(opts Options) *Client {
	hc := opts.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Client{
		http:        hc,
		authBaseURL: firstNonEmpty(opts.AuthBaseURL, defaultAuthBaseURL),
		apiBaseURL:  firstNonEmpty(opts.APIBaseURL, defaultAPIBaseURL),
		clientID:    firstNonEmpty(opts.ClientID, ClientID),
		owner:       firstNonEmpty(opts.Owner, RepoOwner),
		repo:        firstNonEmpty(opts.Repo, RepoName),
		store:       opts.Store,
		logger:      logger,
		now:         now,
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// StartDeviceLogin requests a fresh device_code from GitHub. No
// `scope` parameter is sent because the client identifies a GitHub
// App, whose permissions are configured ahead of time.
func (c *Client) StartDeviceLogin(ctx context.Context) (*DeviceCode, error) {
	form := url.Values{}
	form.Set("client_id", c.clientID)
	resp, err := c.postForm(ctx, c.authBaseURL+"/login/device/code", form)
	if err != nil {
		return nil, fmt.Errorf("device code request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("device code request: status %d", resp.StatusCode)
	}
	var payload struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		ExpiresIn       int    `json:"expires_in"`
		Interval        int    `json:"interval"`
		Error           string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode device code: %w", err)
	}
	if payload.Error != "" {
		return nil, fmt.Errorf("device code request: %s", payload.Error)
	}
	if payload.DeviceCode == "" {
		return nil, errors.New("device code request: empty device_code")
	}
	interval := payload.Interval
	if interval <= 0 {
		interval = 5
	}
	return &DeviceCode{
		DeviceCode:      payload.DeviceCode,
		UserCode:        payload.UserCode,
		VerificationURI: payload.VerificationURI,
		Interval:        interval,
		ExpiresAt:       c.now().Add(time.Duration(payload.ExpiresIn) * time.Second),
	}, nil
}

// PollDeviceLogin attempts a single exchange of device_code for an
// access token. Persists the token on success.
func (c *Client) PollDeviceLogin(ctx context.Context, deviceCode string) (*PollResult, error) {
	if c.store == nil {
		return nil, errors.New("feedback: no token store configured")
	}
	form := url.Values{}
	form.Set("client_id", c.clientID)
	form.Set("device_code", deviceCode)
	form.Set("grant_type", grantDeviceCode)
	resp, err := c.postForm(ctx, c.authBaseURL+"/login/oauth/access_token", form)
	if err != nil {
		return &PollResult{Status: PollStatusError, Error: err.Error()}, nil
	}
	defer resp.Body.Close()
	var payload struct {
		AccessToken           string `json:"access_token"`
		TokenType             string `json:"token_type"`
		ExpiresIn             int    `json:"expires_in"`
		RefreshToken          string `json:"refresh_token"`
		RefreshTokenExpiresIn int    `json:"refresh_token_expires_in"`
		Scope                 string `json:"scope"`
		Error                 string `json:"error"`
		ErrorDescription      string `json:"error_description"`
		Interval              int    `json:"interval"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return &PollResult{Status: PollStatusError, Error: "decode: " + err.Error()}, nil
	}
	switch payload.Error {
	case "":
		// fall through to success path
	case "authorization_pending":
		return &PollResult{Status: PollStatusPending}, nil
	case "slow_down":
		return &PollResult{Status: PollStatusSlowDown, Interval: payload.Interval}, nil
	case "expired_token":
		return &PollResult{Status: PollStatusExpired}, nil
	case "access_denied":
		return &PollResult{Status: PollStatusDenied}, nil
	default:
		return &PollResult{Status: PollStatusError, Error: payload.Error}, nil
	}
	if payload.AccessToken == "" {
		return &PollResult{Status: PollStatusError, Error: "empty access_token"}, nil
	}
	tok := &Token{
		AccessToken:  payload.AccessToken,
		RefreshToken: payload.RefreshToken,
		IssuedAt:     c.now().UTC(),
	}
	if payload.ExpiresIn > 0 {
		tok.ExpiresAt = c.now().Add(time.Duration(payload.ExpiresIn) * time.Second).UTC()
	}
	if payload.RefreshTokenExpiresIn > 0 {
		tok.RefreshExpiresAt = c.now().Add(time.Duration(payload.RefreshTokenExpiresIn) * time.Second).UTC()
	}
	// Resolve login eagerly so the UI shows "verbunden als ..." on
	// the first status read. Failures don't block linkage — the
	// status DTO simply omits the name until a later /user call
	// succeeds.
	if login, err := c.fetchLogin(ctx, tok.AccessToken); err == nil {
		tok.Login = login
	} else {
		c.logger.Warn("feedback: /user after device link failed — login left blank", "err", err)
	}
	if err := c.store.Set(tok); err != nil {
		return &PollResult{Status: PollStatusError, Error: "store: " + err.Error()}, nil
	}
	return &PollResult{Status: PollStatusLinked}, nil
}

// EnsureToken returns a valid access token, refreshing if necessary.
// Returns ErrNotLinked when no token is stored and ErrReauthRequired
// when the refresh token itself has expired.
func (c *Client) EnsureToken(ctx context.Context) (string, error) {
	if c.store == nil {
		return "", ErrNotLinked
	}
	tok, err := c.store.Get()
	if err != nil {
		if errors.Is(err, ErrNoToken) {
			return "", ErrNotLinked
		}
		return "", err
	}
	if !tok.NeedsRefresh(c.now()) {
		return tok.AccessToken, nil
	}
	if tok.RefreshToken == "" || tok.RefreshExpired(c.now()) {
		return "", ErrReauthRequired
	}
	refreshed, err := c.refresh(ctx, tok.RefreshToken)
	if err != nil {
		return "", fmt.Errorf("refresh: %w", err)
	}
	refreshed.Login = tok.Login
	if err := c.store.Set(refreshed); err != nil {
		return "", fmt.Errorf("persist refreshed token: %w", err)
	}
	return refreshed.AccessToken, nil
}

func (c *Client) refresh(ctx context.Context, refreshToken string) (*Token, error) {
	form := url.Values{}
	form.Set("client_id", c.clientID)
	form.Set("grant_type", grantRefreshToken)
	form.Set("refresh_token", refreshToken)
	resp, err := c.postForm(ctx, c.authBaseURL+"/login/oauth/access_token", form)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var payload struct {
		AccessToken           string `json:"access_token"`
		ExpiresIn             int    `json:"expires_in"`
		RefreshToken          string `json:"refresh_token"`
		RefreshTokenExpiresIn int    `json:"refresh_token_expires_in"`
		Error                 string `json:"error"`
		ErrorDescription      string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	if payload.Error != "" {
		// bad_refresh_token / invalid_grant ⇒ caller must re-auth.
		if payload.Error == "bad_refresh_token" || payload.Error == "invalid_grant" {
			return nil, ErrReauthRequired
		}
		return nil, fmt.Errorf("github: %s", payload.Error)
	}
	if payload.AccessToken == "" {
		return nil, errors.New("github: empty access_token in refresh response")
	}
	tok := &Token{
		AccessToken:  payload.AccessToken,
		RefreshToken: payload.RefreshToken,
		IssuedAt:     c.now().UTC(),
	}
	if payload.ExpiresIn > 0 {
		tok.ExpiresAt = c.now().Add(time.Duration(payload.ExpiresIn) * time.Second).UTC()
	}
	if payload.RefreshTokenExpiresIn > 0 {
		tok.RefreshExpiresAt = c.now().Add(time.Duration(payload.RefreshTokenExpiresIn) * time.Second).UTC()
	}
	return tok, nil
}

// CurrentUser returns the GitHub login of the linked account, hitting
// the network. Use the cached Token.Login for cheap status reads.
func (c *Client) CurrentUser(ctx context.Context) (string, error) {
	tok, err := c.EnsureToken(ctx)
	if err != nil {
		return "", err
	}
	return c.fetchLogin(ctx, tok)
}

func (c *Client) fetchLogin(ctx context.Context, accessToken string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiBaseURL+"/user", nil)
	if err != nil {
		return "", err
	}
	c.setGitHubHeaders(req, accessToken)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("/user status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		Login string `json:"login"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	return payload.Login, nil
}

// Logout removes the stored token. Best-effort; a non-existent
// credential entry is not an error.
func (c *Client) Logout(_ context.Context) error {
	if c.store == nil {
		return nil
	}
	return c.store.Delete()
}

// EnsureLabels creates any of the requested labels that don't already
// exist in the target repo. Names not present in DefaultLabels are
// silently ignored — the caller is the source of truth for label
// metadata.
func (c *Client) EnsureLabels(ctx context.Context, names []string) error {
	if len(names) == 0 {
		return nil
	}
	tok, err := c.EnsureToken(ctx)
	if err != nil {
		return err
	}
	specs := make(map[string]LabelSpec, len(DefaultLabels))
	for _, s := range DefaultLabels {
		specs[s.Name] = s
	}
	for _, name := range names {
		spec, ok := specs[name]
		if !ok {
			continue
		}
		if err := c.ensureLabel(ctx, tok, spec); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) ensureLabel(ctx context.Context, accessToken string, spec LabelSpec) error {
	// GitHub URL-escapes the label name in the path. Spaces and ':'
	// pass through QueryEscape correctly for path segments here.
	getURL := fmt.Sprintf("%s/repos/%s/%s/labels/%s",
		c.apiBaseURL, c.owner, c.repo, url.PathEscape(spec.Name))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, getURL, nil)
	if err != nil {
		return err
	}
	c.setGitHubHeaders(req, accessToken)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("label probe %q: %w", spec.Name, err)
	}
	resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusNotFound:
		// fall through to create
	default:
		return fmt.Errorf("label probe %q: status %d", spec.Name, resp.StatusCode)
	}
	body, _ := json.Marshal(map[string]string{
		"name":        spec.Name,
		"color":       spec.Color,
		"description": spec.Description,
	})
	createURL := fmt.Sprintf("%s/repos/%s/%s/labels", c.apiBaseURL, c.owner, c.repo)
	req, err = http.NewRequestWithContext(ctx, http.MethodPost, createURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	c.setGitHubHeaders(req, accessToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err = c.http.Do(req)
	if err != nil {
		return fmt.Errorf("label create %q: %w", spec.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("label create %q: status %d: %s",
			spec.Name, resp.StatusCode, strings.TrimSpace(string(errBody)))
	}
	return nil
}

// CreateIssue posts a new issue in RepoOwner/RepoName. Labels are
// passed through verbatim — EnsureLabels must have been called first
// for any labels that may not exist yet.
func (c *Client) CreateIssue(ctx context.Context, title, body string, labels []string) (*IssueCreated, error) {
	tok, err := c.EnsureToken(ctx)
	if err != nil {
		return nil, err
	}
	payload, _ := json.Marshal(map[string]any{
		"title":  title,
		"body":   body,
		"labels": labels,
	})
	issueURL := fmt.Sprintf("%s/repos/%s/%s/issues", c.apiBaseURL, c.owner, c.repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, issueURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	c.setGitHubHeaders(req, tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("issue create: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("issue create: status %d: %s",
			resp.StatusCode, strings.TrimSpace(string(errBody)))
	}
	var out IssueCreated
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode issue response: %w", err)
	}
	return &out, nil
}

func (c *Client) postForm(ctx context.Context, endpoint string, form url.Values) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return c.http.Do(req)
}

func (c *Client) setGitHubHeaders(req *http.Request, accessToken string) {
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("User-Agent", "Hashpoint-Feedback/1.0")
}

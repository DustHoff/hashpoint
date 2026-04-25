package personio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// HTTPDoer is the subset of *http.Client used by the package.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client speaks to the Personio Attendance API. It transparently refreshes
// OAuth tokens before each call and respects rate-limit headers.
type Client struct {
	BaseURL    string
	ClientID   string
	EmployeeID string

	store  CredentialStore
	http   HTTPDoer
	logger *slog.Logger

	mu          sync.Mutex
	tokenValue  string
	tokenExpiry time.Time
}

// Options configures the client.
type Options struct {
	BaseURL    string
	ClientID   string
	EmployeeID string
	Store      CredentialStore
	HTTPClient HTTPDoer
	Logger     *slog.Logger
	Timeout    time.Duration
}

// New creates a client with sensible defaults applied.
func New(o Options) *Client {
	if o.HTTPClient == nil {
		t := o.Timeout
		if t == 0 {
			t = 10 * time.Second
		}
		o.HTTPClient = &http.Client{Timeout: t}
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	return &Client{
		BaseURL:    o.BaseURL,
		ClientID:   o.ClientID,
		EmployeeID: o.EmployeeID,
		store:      o.Store,
		http:       o.HTTPClient,
		logger:     o.Logger,
	}
}

// AttendanceCreate is the payload sent to POST /v1/company/attendances.
type AttendanceCreate struct {
	EmployeeID string  `json:"employee_id"`
	Date       string  `json:"date"`        // YYYY-MM-DD
	StartTime  string  `json:"start_time"`  // HH:MM
	EndTime    string  `json:"end_time"`    // HH:MM
	Comment    string  `json:"comment,omitempty"`
	ProjectID  string  `json:"project_id,omitempty"`
	ActivityID string  `json:"activity_id,omitempty"`
	Break      *int    `json:"break,omitempty"` // minutes
}

// AttendanceCreateResult is what the API returns for a successful create.
type AttendanceCreateResult struct {
	ID string `json:"id"`
}

// CreateAttendance posts a single attendance record.
func (c *Client) CreateAttendance(ctx context.Context, a AttendanceCreate) (*AttendanceCreateResult, error) {
	body, err := json.Marshal(map[string]any{"attendances": []AttendanceCreate{a}})
	if err != nil {
		return nil, err
	}

	resp, err := c.doWithRetry(ctx, http.MethodPost, "/company/attendances", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("personio: create attendance: status %d: %s", resp.StatusCode, string(raw))
	}

	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode create response: %w", err)
	}
	if len(parsed.Data) == 0 {
		return nil, errors.New("personio: empty data array in create response")
	}
	return &AttendanceCreateResult{ID: parsed.Data[0].ID}, nil
}

// UpdateAttendance patches an existing attendance record.
func (c *Client) UpdateAttendance(ctx context.Context, id string, a AttendanceCreate) error {
	body, err := json.Marshal(a)
	if err != nil {
		return err
	}
	resp, err := c.doWithRetry(ctx, http.MethodPatch, "/company/attendances/"+id, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("personio: update attendance: status %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}

func (c *Client) doWithRetry(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	const maxAttempts = 3
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		token, err := c.token(ctx)
		if err != nil {
			return nil, fmt.Errorf("auth: %w", err)
		}
		req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = err
			c.backoff(ctx, attempt, 0)
			continue
		}
		if resp.StatusCode == http.StatusUnauthorized {
			resp.Body.Close()
			c.invalidateToken()
			continue
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode/100 == 5 {
			retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
			resp.Body.Close()
			lastErr = fmt.Errorf("transient status %d", resp.StatusCode)
			c.backoff(ctx, attempt, retryAfter)
			continue
		}
		return resp, nil
	}
	if lastErr == nil {
		lastErr = errors.New("personio: retries exhausted")
	}
	return nil, lastErr
}

func (c *Client) backoff(ctx context.Context, attempt int, retryAfter time.Duration) {
	if retryAfter == 0 {
		retryAfter = time.Duration(math.Pow(2, float64(attempt))) * time.Second
	}
	select {
	case <-ctx.Done():
	case <-time.After(retryAfter):
	}
}

func parseRetryAfter(h string) time.Duration {
	if h == "" {
		return 0
	}
	if secs, err := strconv.Atoi(h); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(h); err == nil {
		d := time.Until(t)
		if d > 0 {
			return d
		}
	}
	return 0
}

func (c *Client) invalidateToken() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tokenValue = ""
	c.tokenExpiry = time.Time{}
}

// token returns a cached or freshly fetched OAuth access token.
func (c *Client) token(ctx context.Context) (string, error) {
	c.mu.Lock()
	if c.tokenValue != "" && time.Now().Before(c.tokenExpiry.Add(-30*time.Second)) {
		v := c.tokenValue
		c.mu.Unlock()
		return v, nil
	}
	c.mu.Unlock()

	if c.store == nil {
		return "", ErrSecretNotSet
	}
	secret, err := c.store.GetSecret()
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(map[string]string{
		"client_id":     c.ClientID,
		"client_secret": secret,
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/auth", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("personio auth: status %d: %s", resp.StatusCode, string(raw))
	}
	var parsed struct {
		Data struct {
			Token   string `json:"token"`
			Expires int64  `json:"expires_in"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("decode auth: %w", err)
	}
	if parsed.Data.Token == "" {
		return "", errors.New("personio auth: empty token")
	}
	exp := time.Now().Add(time.Hour)
	if parsed.Data.Expires > 0 {
		exp = time.Now().Add(time.Duration(parsed.Data.Expires) * time.Second)
	}
	c.mu.Lock()
	c.tokenValue = parsed.Data.Token
	c.tokenExpiry = exp
	c.mu.Unlock()
	return parsed.Data.Token, nil
}

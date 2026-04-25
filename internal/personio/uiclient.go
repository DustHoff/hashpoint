package personio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

// HTTPDoer is the subset of *http.Client used by the package.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// UIClient talks to the Personio internal/UI API on behalf of an
// interactively-authenticated user. All requests carry the session cookies
// captured during login plus the matching x-athena-xsrf-token header derived
// from the XSRF cookie value (URL-decoded).
type UIClient struct {
	BaseURL string
	Session *Session

	http   *http.Client
	logger *slog.Logger
}

// UIClientOptions configures a UIClient.
type UIClientOptions struct {
	Session *Session
	HTTP    *http.Client
	Logger  *slog.Logger
	Timeout time.Duration
}

// NewUIClient wires a UIClient around a captured session. The cookies are
// loaded into a fresh cookie jar so the standard library handles attaching
// them to subsequent requests.
func NewUIClient(opts UIClientOptions) (*UIClient, error) {
	if opts.Session == nil {
		return nil, ErrNoSession
	}
	host := strings.TrimSpace(opts.Session.AppHost)
	if host == "" {
		// Older sessions may pre-date the AppHost capture — fall back to
		// the standard <tenant>.app.personio.com pattern.
		if t := strings.TrimSpace(opts.Session.Tenant); t != "" {
			host = t + ".app.personio.com"
		} else {
			return nil, errors.New("personio uiclient: session has no host")
		}
	}
	base := "https://" + host

	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("cookiejar: %w", err)
	}
	cookies := opts.Session.HTTPCookies()
	// Cookies are expressed against multiple domains (.personio.de,
	// .personio.com); seed the jar against both so anything attaches
	// regardless of which one the cookie was scoped to.
	for _, dom := range []string{"https://" + host + "/", "https://personio.de/", "https://personio.com/"} {
		if u, err := url.Parse(dom); err == nil {
			jar.SetCookies(u, cookies)
		}
	}

	cli := opts.HTTP
	if cli == nil {
		t := opts.Timeout
		if t == 0 {
			t = 15 * time.Second
		}
		cli = &http.Client{Timeout: t}
	}
	cli.Jar = jar

	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &UIClient{
		BaseURL: base,
		Session: opts.Session,
		http:    cli,
		logger:  logger,
	}, nil
}

// ErrSessionExpired indicates that Personio rejected the current cookies and
// a fresh interactive login is required.
var ErrSessionExpired = errors.New("personio: session expired — please re-authenticate")

// NavigationContext is the (subset of the) /api/v1/navigation/context
// response containing the employee identifier we need.
type NavigationContext struct {
	EmployeeID int64 `json:"employee_id"`
}

// FetchEmployeeID resolves the authenticated user's numeric employee id by
// calling /api/v1/navigation/context. Real-world response shape:
//
//	{"success":true,"data":{"user":{"id":10076878, ...}, ...}}
func (c *UIClient) FetchEmployeeID(ctx context.Context) (int64, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/v1/navigation/context", nil)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return 0, statusErr("fetch employee id", resp)
	}
	var parsed struct {
		Data struct {
			User struct {
				ID int64 `json:"id"`
			} `json:"user"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return 0, fmt.Errorf("decode navigation context: %w", err)
	}
	if parsed.Data.User.ID == 0 {
		return 0, errors.New("personio: navigation/context did not include user id")
	}
	return parsed.Data.User.ID, nil
}

// Timecard is one row of the timesheet response. We only model the fields we
// rely on; many more (overtime, alerts, target_hours, …) are omitted.
type Timecard struct {
	DayID   string `json:"day_id"`
	Date    string `json:"date"`
	State   string `json:"state"` // "trackable", "locked", "non_trackable", …
	IsOff   bool   `json:"is_off_day"`
	Periods []struct {
		ID        string `json:"id"`
		Start     string `json:"start"`
		End       string `json:"end"`
		ProjectID *int64 `json:"project_id"`
		Comment   string `json:"comment"`
		Type      string `json:"type"`
	} `json:"periods"`
}

// Trackable reports whether Personio allows the user to add or modify
// attendance for this day.
func (t Timecard) Trackable() bool {
	switch strings.ToLower(t.State) {
	case "trackable", "open", "rejected":
		return true
	default:
		return false
	}
}

// FetchTimesheet returns timecards for [from, to] (inclusive). Personio's
// timesheet endpoint expects dates in the IANA timezone the caller wants;
// we always pass Europe/Berlin (which is what the web UI also uses).
func (c *UIClient) FetchTimesheet(ctx context.Context, employeeID int64, from, to time.Time) ([]Timecard, error) {
	if employeeID == 0 {
		return nil, errors.New("personio: employee id is zero")
	}
	q := url.Values{}
	q.Set("start_date", from.Local().Format("2006-01-02"))
	q.Set("end_date", to.Local().Format("2006-01-02"))
	q.Set("timezone", "Europe/Berlin")
	q.Set("source", "OVERTIME_SERVICE")
	path := fmt.Sprintf("/svc/attendance-bff/v1/timesheet/%d?%s", employeeID, q.Encode())
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, statusErr("fetch timesheet", resp)
	}
	var parsed struct {
		Timecards []Timecard `json:"timecards"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode timesheet: %w", err)
	}
	return parsed.Timecards, nil
}

// Period is one row inside a day's "periods" array sent to PUT
// /svc/attendance-api/v1/days/{day_id}.
type Period struct {
	ID            string `json:"id"`
	Comment       string `json:"comment"`
	PeriodType    string `json:"period_type"` // "work" or "break"
	ProjectID     *int64 `json:"project_id"`
	Start         string `json:"start"` // local-naive YYYY-MM-DDTHH:MM:SS
	End           string `json:"end"`
	AutoGenerated bool   `json:"auto_generated"`
}

// SetDayPayload is the body of PUT /svc/attendance-api/v1/days/{day_id}.
// The upstream API also accepts a few flag fields the web UI always sends;
// we mirror them so requests look identical.
type SetDayPayload struct {
	EmployeeID      int64    `json:"employee_id"`
	Periods         []Period `json:"periods"`
	OriginalPeriods []Period `json:"original_periods"`
	Geolocation     any      `json:"geolocation"`
	IsFromClockOut  bool     `json:"is_from_clock_out"`
}

// SetDay overwrites the periods for a calendar day. Both new and existing
// days are addressed by UUID; if the day did not yet exist on Personio, the
// caller passes a fresh client-generated UUID v4 and Personio creates it.
// The autoFix=true / usedInTimesheet=true query params match the values
// the official web UI sends.
func (c *UIClient) SetDay(ctx context.Context, dayID string, payload SetDayPayload) error {
	if dayID == "" {
		return errors.New("personio: day id is empty")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	resp, err := c.do(ctx, http.MethodPut,
		"/svc/attendance-api/v1/days/"+dayID+"?autoFix=true&usedInTimesheet=true", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return statusErr("set day", resp)
	}
	return nil
}

// do is the request workhorse: it injects content/accept headers, attaches
// the x-athena-xsrf-token derived from the XSRF cookie, sends the request
// through the client's cookie jar, and translates 401 / login redirects
// into ErrSessionExpired so callers can prompt a fresh login.
func (c *UIClient) do(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	full := c.BaseURL + path
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, full, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if t := c.Session.XSRFToken(); t != "" {
		req.Header.Set("x-athena-xsrf-token", t)
	}
	req.Header.Set("Origin", c.BaseURL)
	req.Header.Set("Referer", c.BaseURL+"/")

	prev := c.http.CheckRedirect
	c.http.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	defer func() { c.http.CheckRedirect = prev }()

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		c.logger.Warn("personio: auth rejected", "status", resp.StatusCode, "body", truncate(string(raw), 200))
		return nil, ErrSessionExpired
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		loc := resp.Header.Get("Location")
		if loc != "" {
			lu, _ := url.Parse(loc)
			if lu != nil && (strings.HasPrefix(lu.Path, "/login") || strings.Contains(lu.Path, "/auth")) {
				resp.Body.Close()
				return nil, ErrSessionExpired
			}
		}
	}
	return resp, nil
}

func statusErr(op string, resp *http.Response) error {
	raw, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("personio: %s: status %d: %s", op, resp.StatusCode, truncate(string(raw), 300))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

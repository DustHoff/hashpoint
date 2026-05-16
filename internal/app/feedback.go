package app

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dusthoff/hashpoint/internal/feedback"
)

// pendingDeviceFlow is the in-flight handshake started by
// FeedbackStartDeviceLogin and consumed by FeedbackPollDeviceLogin.
// The device code itself never leaves the backend — the frontend only
// sees the user code and the verification URI.
type pendingDeviceFlow struct {
	code      *feedback.DeviceCode
	startedAt time.Time
}

// feedbackState carries the App-side bookkeeping for the GitHub
// Feedback flow. Lives behind feedbackMu so concurrent UI calls from
// the React layer don't race on the device-code handshake.
type feedbackState struct {
	mu      sync.Mutex
	client  *feedback.Client
	pending *pendingDeviceFlow
}

// initFeedback constructs the per-app feedback client lazily on first
// use. The token store falls back to the platform default when Deps
// did not provide one — tests inject a memory store directly.
func (a *App) initFeedback() *feedback.Client {
	a.feedback.mu.Lock()
	defer a.feedback.mu.Unlock()
	if a.feedback.client != nil {
		return a.feedback.client
	}
	store := a.deps.FeedbackTokens
	if store == nil {
		store = feedback.NewDefaultTokenStore()
	}
	a.feedback.client = feedback.NewClient(feedback.Options{
		Store:  store,
		Logger: a.logger,
	})
	return a.feedback.client
}

// FeedbackStatusDTO is what the Feedback tab reads to decide between
// the Login button and the "verbunden als ..." panel. CheckedAt is
// always set so the UI can render "zuletzt geprüft" even on the
// disconnected branch.
type FeedbackStatusDTO struct {
	Linked    bool      `json:"linked"`
	Login     string    `json:"login,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
}

// FeedbackStatus reports whether a GitHub token is on file. Cheap —
// no network call; uses the cached login from the token blob.
func (a *App) FeedbackStatus() FeedbackStatusDTO {
	now := time.Now().UTC()
	c := a.initFeedback()
	if _, err := c.EnsureToken(a.ctx); err != nil {
		switch {
		case errors.Is(err, feedback.ErrNotLinked):
			return FeedbackStatusDTO{CheckedAt: now, Reason: "nicht verbunden"}
		case errors.Is(err, feedback.ErrReauthRequired):
			return FeedbackStatusDTO{CheckedAt: now, Reason: "erneute Anmeldung erforderlich"}
		}
		return FeedbackStatusDTO{CheckedAt: now, Reason: err.Error()}
	}
	tok, err := a.feedbackTokenForStatus()
	if err != nil {
		return FeedbackStatusDTO{CheckedAt: now, Reason: err.Error()}
	}
	return FeedbackStatusDTO{Linked: true, Login: tok.Login, CheckedAt: now}
}

// feedbackTokenForStatus reads the persisted token blob (post
// EnsureToken refresh) so FeedbackStatus can surface the cached
// login. Wrapped to keep the lock-handling out of the hot path.
func (a *App) feedbackTokenForStatus() (*feedback.Token, error) {
	store := a.deps.FeedbackTokens
	if store == nil {
		store = feedback.NewDefaultTokenStore()
	}
	return store.Get()
}

// FeedbackDeviceCodeDTO is the public face of a Device-Flow start.
// The device_code stays in the backend; the user_code and verification
// URI are everything the React modal needs.
type FeedbackDeviceCodeDTO struct {
	UserCode        string    `json:"user_code"`
	VerificationURI string    `json:"verification_uri"`
	IntervalSeconds int       `json:"interval_seconds"`
	ExpiresAt       time.Time `json:"expires_at"`
}

// FeedbackStartDeviceLogin kicks off a fresh Device-Flow handshake.
// A second call while a handshake is already in flight discards the
// previous one — the user may have closed the modal and restarted.
func (a *App) FeedbackStartDeviceLogin() (*FeedbackDeviceCodeDTO, error) {
	c := a.initFeedback()
	dc, err := c.StartDeviceLogin(a.ctx)
	if err != nil {
		a.logger.Warn("feedback: device login start failed", "err", err)
		return nil, err
	}
	a.feedback.mu.Lock()
	a.feedback.pending = &pendingDeviceFlow{code: dc, startedAt: time.Now().UTC()}
	a.feedback.mu.Unlock()
	a.logger.Info("feedback: device login started", "user_code", dc.UserCode)
	return &FeedbackDeviceCodeDTO{
		UserCode:        dc.UserCode,
		VerificationURI: dc.VerificationURI,
		IntervalSeconds: dc.Interval,
		ExpiresAt:       dc.ExpiresAt,
	}, nil
}

// FeedbackPollResultDTO mirrors feedback.PollResult for the frontend.
// The Status string discriminates the next UI action (keep polling,
// show error, transition to connected).
type FeedbackPollResultDTO struct {
	Status   string `json:"status"`
	Interval int    `json:"interval,omitempty"`
	Error    string `json:"error,omitempty"`
}

// FeedbackPollDeviceLogin runs one /access_token poll against the
// in-flight device code. The frontend invokes this on the interval
// returned by Start; widening (slow_down) and expiry (expired_token)
// are reported back as status fields.
func (a *App) FeedbackPollDeviceLogin() (*FeedbackPollResultDTO, error) {
	a.feedback.mu.Lock()
	pending := a.feedback.pending
	a.feedback.mu.Unlock()
	if pending == nil {
		return nil, errors.New("feedback: kein aktiver Login — bitte erneut starten")
	}
	c := a.initFeedback()
	res, err := c.PollDeviceLogin(a.ctx, pending.code.DeviceCode)
	if err != nil {
		return nil, err
	}
	switch res.Status {
	case feedback.PollStatusLinked, feedback.PollStatusDenied, feedback.PollStatusExpired:
		a.feedback.mu.Lock()
		a.feedback.pending = nil
		a.feedback.mu.Unlock()
	}
	if res.Status == feedback.PollStatusLinked {
		a.logger.Info("feedback: device login linked")
	}
	return &FeedbackPollResultDTO{
		Status:   string(res.Status),
		Interval: res.Interval,
		Error:    res.Error,
	}, nil
}

// FeedbackLogout removes the persisted token and clears any in-flight
// device-code handshake.
func (a *App) FeedbackLogout() error {
	c := a.initFeedback()
	a.feedback.mu.Lock()
	a.feedback.pending = nil
	a.feedback.mu.Unlock()
	if err := c.Logout(a.ctx); err != nil {
		return fmt.Errorf("logout: %w", err)
	}
	a.logger.Info("feedback: logged out")
	return nil
}

// FeedbackInputDTO is the form payload from the React Feedback tab.
// Validation lives in toFeedbackInput so the bindings stay thin and
// errors surface through the same path Preview and Submit share.
type FeedbackInputDTO struct {
	Title       string `json:"title"`
	Category    string `json:"category"`
	Severity    string `json:"severity"`
	Description string `json:"description"`
	Expected    string `json:"expected"`
	Actual      string `json:"actual"`
	Repro       string `json:"repro"`
	IncludeLog  bool   `json:"include_log"`
	LogWindow   string `json:"log_window"`
}

// FeedbackSubmitResultDTO is returned to the UI on a successful
// submit. HTMLURL is what the success toast turns into a link.
type FeedbackSubmitResultDTO struct {
	Number  int    `json:"number"`
	HTMLURL string `json:"html_url"`
}

// FeedbackPreview returns the rendered Markdown body the Submit
// button would post. Lets the user see what the log looks like before
// the issue is created — critical for the privacy story.
func (a *App) FeedbackPreview(in FeedbackInputDTO) (string, error) {
	input, err := a.toFeedbackInput(in)
	if err != nil {
		return "", err
	}
	return feedback.Render(input), nil
}

// FeedbackSubmit auto-creates any missing labels, posts the issue,
// and returns the GitHub URL. Errors at any stage are surfaced
// verbatim — the form-level reauth/network branches are distinct
// codepaths the UI can recover from.
func (a *App) FeedbackSubmit(in FeedbackInputDTO) (*FeedbackSubmitResultDTO, error) {
	input, err := a.toFeedbackInput(in)
	if err != nil {
		return nil, err
	}
	c := a.initFeedback()
	labels := feedback.LabelsFor(input)
	if err := c.EnsureLabels(a.ctx, labels); err != nil {
		a.logger.Warn("feedback: ensure labels failed", "err", err)
		return nil, fmt.Errorf("labels: %w", err)
	}
	body := feedback.Render(input)
	out, err := c.CreateIssue(a.ctx, strings.TrimSpace(input.Title), body, labels)
	if err != nil {
		a.logger.Warn("feedback: issue create failed", "err", err)
		return nil, err
	}
	a.logger.Info("feedback: issue created", "number", out.Number, "url", out.HTMLURL)
	return &FeedbackSubmitResultDTO{Number: out.Number, HTMLURL: out.HTMLURL}, nil
}

// toFeedbackInput validates the DTO and pulls the optional log tail.
// Validation is deliberately strict: a bad category or severity
// would be silently downgraded by the label/severity fallbacks, so
// catch them at the binding boundary instead.
func (a *App) toFeedbackInput(in FeedbackInputDTO) (feedback.Input, error) {
	title := strings.TrimSpace(in.Title)
	if title == "" {
		// User-facing German message; the staticcheck capitalisation rule
		// doesn't apply to a proper-noun-style sentence start.
		return feedback.Input{}, errors.New("Titel darf nicht leer sein") //nolint:staticcheck // ST1005
	}
	cat, err := parseFeedbackCategory(in.Category)
	if err != nil {
		return feedback.Input{}, err
	}
	sev, err := parseFeedbackSeverity(in.Severity)
	if err != nil {
		return feedback.Input{}, err
	}
	var window feedback.LogWindow
	var logTail []byte
	if in.IncludeLog {
		w, err := parseFeedbackWindow(in.LogWindow)
		if err != nil {
			return feedback.Input{}, err
		}
		window = w
		tail, err := a.readFeedbackLog(window)
		if err != nil {
			a.logger.Warn("feedback: log tail failed — submitting without log", "err", err)
		} else {
			logTail = tail
		}
	}
	return feedback.Input{
		Title:       title,
		Category:    cat,
		Severity:    sev,
		Description: in.Description,
		Expected:    in.Expected,
		Actual:      in.Actual,
		Repro:       in.Repro,
		About: feedback.AboutInfo{
			Version:   a.deps.Version.Version,
			Commit:    a.deps.Version.Commit,
			BuildDate: a.deps.Version.BuildDate,
		},
		LogTail:   logTail,
		LogWindow: window,
	}, nil
}

func (a *App) readFeedbackLog(window feedback.LogWindow) ([]byte, error) {
	if a.deps.LogDir == "" {
		return nil, errors.New("kein LogDir konfiguriert")
	}
	path := filepath.Join(a.deps.LogDir, feedback.LogFileName)
	return feedback.ReadLogTail(a.ctx, path, window, time.Now())
}

func parseFeedbackCategory(s string) (feedback.Category, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "bug":
		return feedback.CategoryBug, nil
	case "feature":
		return feedback.CategoryFeature, nil
	case "question":
		return feedback.CategoryQuestion, nil
	}
	return "", fmt.Errorf("unbekannte Kategorie: %q", s)
}

func parseFeedbackSeverity(s string) (feedback.Severity, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "low":
		return feedback.SeverityLow, nil
	case "medium":
		return feedback.SeverityMedium, nil
	case "high":
		return feedback.SeverityHigh, nil
	case "critical":
		return feedback.SeverityCritical, nil
	}
	return "", fmt.Errorf("unbekannte Schweregradstufe: %q", s)
}

func parseFeedbackWindow(s string) (feedback.LogWindow, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "today", "":
		return feedback.LogWindowToday, nil
	case "1h":
		return feedback.LogWindowHour, nil
	case "24h":
		return feedback.LogWindowDay, nil
	}
	return "", fmt.Errorf("unbekannter Log-Zeitraum: %q", s)
}

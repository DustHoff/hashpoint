// Package config loads, validates and persists the user-configurable
// TimeTracker settings stored as TOML in %APPDATA%\TimeTracker\config.toml.
//
// Personio authentication is cookie-based (session captured via the
// CDP-driven login flow); cookies and the resolved employee id are
// kept in the Windows Credential Manager — never in this file and
// never logged.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// urlParse is a thin alias kept so the rest of the package can call it
// without importing net/url at the top.
func urlParse(s string) (*url.URL, error) { return url.Parse(s) }

// Config is the persisted user configuration.
//
// JSON tags are required because Wails marshals bound-method return values
// via encoding/json — without them the React layer would receive Go's
// PascalCase field names instead of the snake_case keys it expects, and
// nested property access would throw at render time.
type Config struct {
	Tracking      TrackingConfig      `toml:"tracking"      json:"tracking"`
	Personio      PersonioConfig      `toml:"personio"      json:"personio"`
	Entra         EntraConfig         `toml:"entra"         json:"entra"`
	QuickTag      QuickTagConfig      `toml:"quick_tag"     json:"quick_tag"`
	Communication CommunicationConfig `toml:"communication" json:"communication"`
	WorkSchedule  WorkScheduleConfig  `toml:"work_schedule" json:"work_schedule"`
	OnCall        OnCallConfig        `toml:"on_call"       json:"on_call"`
	// Plugins is opaque per-plugin field config. Keys match the plugin's
	// directory name under PluginsDir; values are the {field: value} pairs
	// the plugin's manifest declares under config_schema.fields. Secrets are
	// never stored here — they live in Windows Credential Manager under
	// target "TimeTracker:plugin:<plugin-name>:<secret-key>" and are
	// surfaced to the plugin via opaque SecretHandles.
	Plugins map[string]map[string]string `toml:"plugins" json:"plugins"`
}

// OnCallConfig configures the on-call ("Rufbereitschaft") documentation
// pipeline. When a tag block overlaps off-hours per WorkScheduleConfig AND
// its tag (or any ancestor) is in TagIDs, the orchestrator enqueues a
// documentation row that the user fills out on the Rufbereitschaft tab.
// Empty TagIDs ⇒ feature dormant (no docs are ever enqueued).
type OnCallConfig struct {
	TagIDs []int64 `toml:"tag_ids" json:"tag_ids"`
}

// TrackingConfig holds polling/idle parameters.
type TrackingConfig struct {
	PollIntervalSec  int `toml:"poll_interval_sec"  json:"poll_interval_sec"`
	IdleThresholdMin int `toml:"idle_threshold_min" json:"idle_threshold_min"`
	// Enabled controls whether the foreground-window polling loop runs. When
	// false the tracker stays paused (no focus blocks are created); the tray
	// "Pause Tracking" toggle is a transient runtime override on top of this.
	Enabled bool `toml:"enabled" json:"enabled"`
	// TagBlockGranularityMin quantizes the duration of every tag-block sent to
	// Personio to a multiple of this many minutes — a started X-minute slot
	// counts as a full X minutes. With 0 (default) no rounding is applied;
	// a typical value is 15 ("Viertelstunden-Buchung"). Range [0,60].
	TagBlockGranularityMin int `toml:"tag_block_granularity_min" json:"tag_block_granularity_min"`
}

// PersonioConfig holds the Personio tenant subdomain. The session cookies
// captured via the CDP login flow live in the Windows Credential Manager.
type PersonioConfig struct {
	// Tenant is the Personio subdomain (e.g. "onesi" → https://onesi.personio.de).
	// May be left empty on first start; populated via the in-app settings UI.
	Tenant string `toml:"tenant" json:"tenant"`
}

// EntraConfig parameterises the optional Microsoft Entra ID authentication
// feature used to obtain delegated tokens for Microsoft Graph (SharePoint,
// calendar) and Entra-protected custom APIs. Both fields are user-supplied
// and may be left empty: when ClientID or TenantID is missing the entire
// feature is dormant — no MSAL client is built, no auth code runs at
// startup, and the related UI surfaces stay disabled.
//
// Both values are public identifiers (Application (client) ID and Directory
// (tenant) ID from the App Registration in the Azure portal). No client
// secret is ever stored — the authentication uses a public-client / native
// flow with PKCE and the system browser. The persistent token cache lives
// next to the database under %LOCALAPPDATA%\TimeTracker\auth and is
// DPAPI-encrypted (CurrentUser scope), so it never holds plaintext tokens.
type EntraConfig struct {
	// ClientID is the Application (client) ID GUID from the Entra ID
	// App Registration ("Mobile and desktop applications" platform with
	// Public Client Flows enabled).
	ClientID string `toml:"client_id" json:"client_id"`
	// TenantID is the Directory (tenant) ID GUID. We deliberately require
	// a concrete tenant — single-tenant by design; the values "common"
	// or "organizations" are rejected by Validate().
	TenantID string `toml:"tenant_id" json:"tenant_id"`
}

// Configured reports whether both ClientID and TenantID are filled in.
// Callers that gate UI entry points or skip building the MSAL client at
// startup should consult this rather than attempting an AcquireToken call.
func (e EntraConfig) Configured() bool {
	return strings.TrimSpace(e.ClientID) != "" && strings.TrimSpace(e.TenantID) != ""
}

// Authority returns the OIDC authority URL used by MSAL, or the empty
// string when no tenant is configured. Always single-tenant — the
// public-cloud login host is hard-coded.
func (e EntraConfig) Authority() string {
	t := strings.TrimSpace(e.TenantID)
	if t == "" {
		return ""
	}
	return "https://login.microsoftonline.com/" + t
}

// QuickTagConfig configures the global Quick-Tag-Picker hotkey. When
// Enabled is true, hashpoint registers Hotkey as a system-wide hotkey via
// RegisterHotKey; pressing it shows a small popup at the cursor display's
// bottom-right, listing the user's recently-used tags numbered 0–9.
//
// Hotkey is a human-readable string of the form "Ctrl+Alt+T" — modifiers
// (Ctrl, Alt, Shift, Win) joined with `+`, terminated by a single key
// (letters A-Z, digits 0-9, or function keys F1-F24). Parsing is
// case-insensitive; serialisation re-renders to the canonical case.
type QuickTagConfig struct {
	Enabled bool   `toml:"enabled" json:"enabled"`
	Hotkey  string `toml:"hotkey"  json:"hotkey"`
}

// CommunicationConfig configures parallel tracking of communication processes
// (e.g. Microsoft Teams). While a configured process owns at least one
// visible top-level window, the tracker keeps a parallel "communication
// track" open in addition to the regular focused-window track, and any
// auto-tag rule matching that window opens a comm-driven tag block that
// overrides focus-driven auto-tags for the same time range. ProcessNames are
// compared case-insensitively against `process_name` (i.e. the basename of
// the executable, e.g. "teams.exe").
//
// TitleExcludePhrases is a global exclusion list applied to comm-process
// window titles. If a window's title contains any of these phrases (case-
// insensitive substring match), the window is treated as a regular process —
// no comm-track and no comm-driven auto-tag override. Re-evaluated on every
// poll tick so a runtime title change immediately closes/reopens the comm
// track. See spec §2.1a.
type CommunicationConfig struct {
	ProcessNames        []string `toml:"process_names"         json:"process_names"`
	TitleExcludePhrases []string `toml:"title_exclude_phrases" json:"title_exclude_phrases"`
}

// NormalizeProcessNames returns a sanitized copy of n: each entry is trimmed,
// lower-cased, and empties / duplicates are removed. The slice keeps the
// caller's order so the UI can present it stably.
func NormalizeProcessNames(n []string) []string {
	if len(n) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(n))
	out := make([]string, 0, len(n))
	for _, raw := range n {
		s := strings.ToLower(strings.TrimSpace(raw))
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// WorkScheduleConfig captures the user's nominal working hours and working
// weekdays. The values are purely informational today: they seed the calendar
// view's "is this a workday" shading and are surfaced in the settings UI so
// downstream features (sync filtering, expected-hours reporting) can consume
// them later without another config migration.
//
// StartHour and EndHour are integer hour-of-day values in the local timezone:
// StartHour is inclusive, EndHour is exclusive, so the default 8..18 means
// "08:00 up to but not including 18:00" — i.e. a 10-hour window. Validate()
// enforces 0 <= StartHour < EndHour <= 24.
//
// WorkDays is a list of canonical English weekday short names ("Mon" .. "Sun").
// English keys keep the on-disk TOML locale-independent; the UI maps them to
// localized German labels (Mo, Di, ...). An empty WorkDays list is legal and
// means "no day is a working day"; the calendar then renders every cell
// muted. Order in the slice does not matter — NormalizeWorkDays sorts to
// canonical Mon→Sun order and drops duplicates / unknown values.
type WorkScheduleConfig struct {
	StartHour int      `toml:"start_hour" json:"start_hour"`
	EndHour   int      `toml:"end_hour"   json:"end_hour"`
	WorkDays  []string `toml:"work_days"  json:"work_days"`
}

// workDayOrder is the canonical Mon→Sun ordering used for serialisation and
// the time.Weekday → string mapping below. The package-private slice lets
// NormalizeWorkDays sort deterministically without a per-call allocation.
var workDayOrder = []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}

// workDayByWeekday translates Go's time.Weekday (Sunday=0) to the canonical
// short name used in TOML / JSON.
var workDayByWeekday = map[time.Weekday]string{
	time.Monday:    "Mon",
	time.Tuesday:   "Tue",
	time.Wednesday: "Wed",
	time.Thursday:  "Thu",
	time.Friday:    "Fri",
	time.Saturday:  "Sat",
	time.Sunday:    "Sun",
}

// canonicalWorkDay maps any accepted spelling ("mon", "Monday", "MON") to the
// canonical short form. Returns "" for unknown input so the validator can
// surface a clear error message instead of silently dropping the entry.
func canonicalWorkDay(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "mon", "monday":
		return "Mon"
	case "tue", "tues", "tuesday":
		return "Tue"
	case "wed", "weds", "wednesday":
		return "Wed"
	case "thu", "thur", "thurs", "thursday":
		return "Thu"
	case "fri", "friday":
		return "Fri"
	case "sat", "saturday":
		return "Sat"
	case "sun", "sunday":
		return "Sun"
	}
	return ""
}

// NormalizeWorkDays returns a sanitized copy of n: each entry is trimmed and
// case-normalised to the canonical short form ("Mon" .. "Sun"); unknown
// entries are dropped; duplicates are collapsed; the result is sorted in
// canonical Mon→Sun order so the on-disk TOML stays stable across saves.
// A nil / empty input returns nil so callers can distinguish "no days
// configured" from "explicitly all days unselected" — both legal states.
func NormalizeWorkDays(n []string) []string {
	if len(n) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(n))
	for _, raw := range n {
		c := canonicalWorkDay(raw)
		if c == "" {
			continue
		}
		seen[c] = struct{}{}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for _, d := range workDayOrder {
		if _, ok := seen[d]; ok {
			out = append(out, d)
		}
	}
	return out
}

// IsWorkDay reports whether the local-time weekday of t is in the configured
// WorkDays list. Returns false for an empty list (no days configured).
func (w WorkScheduleConfig) IsWorkDay(t time.Time) bool {
	name, ok := workDayByWeekday[t.Weekday()]
	if !ok {
		return false
	}
	for _, d := range w.WorkDays {
		if d == name {
			return true
		}
	}
	return false
}

// NormalizeTitleExcludePhrases returns a sanitized copy of n: trimmed,
// empties dropped, deduplicated case-insensitively. Original casing is kept
// because the phrases are surfaced to the user verbatim in the settings UI;
// the comparison itself is case-insensitive at match time.
func NormalizeTitleExcludePhrases(n []string) []string {
	if len(n) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(n))
	out := make([]string, 0, len(n))
	for _, raw := range n {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		key := strings.ToLower(s)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, s)
	}
	return out
}

// Paths bundles resolved on-disk locations.
type Paths struct {
	ConfigFile string // %APPDATA%\TimeTracker\config.toml
	DataDir    string // %LOCALAPPDATA%\TimeTracker
	DBFile     string // %LOCALAPPDATA%\TimeTracker\data.db
	LogDir     string // %LOCALAPPDATA%\TimeTracker\log
	// AuthDir holds DPAPI-encrypted blobs for optional authentication
	// features (currently the Entra ID MSAL token cache). User-only
	// permissions inherited from %LOCALAPPDATA%; the file is encrypted
	// regardless.
	AuthDir string // %LOCALAPPDATA%\TimeTracker\auth
	// PluginsDir holds installed plugin binaries (and their manifest.toml).
	// Each plugin lives under PluginsDir\<plugin-name>\.
	PluginsDir string // %APPDATA%\TimeTracker\plugins
}

// PollInterval returns the poll interval as a duration.
func (t TrackingConfig) PollInterval() time.Duration {
	return time.Duration(t.PollIntervalSec) * time.Second
}

// IdleThreshold returns the idle threshold as a duration.
func (t TrackingConfig) IdleThreshold() time.Duration {
	return time.Duration(t.IdleThresholdMin) * time.Minute
}

// TagBlockGranularity returns the rounding step for tag-block durations as a
// duration. A return value of 0 disables rounding.
func (t TrackingConfig) TagBlockGranularity() time.Duration {
	return time.Duration(t.TagBlockGranularityMin) * time.Minute
}

// SnapStart returns the largest grid boundary <= ts with the rounding step
// (TagBlockGranularity) anchored at local midnight — the inverse of SnapEnd.
// Returns ts unchanged when granularity is 0.
func (t TrackingConfig) SnapStart(ts time.Time) time.Time {
	step := t.TagBlockGranularity()
	if step <= 0 {
		return ts
	}
	local := ts.Local()
	midnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
	delta := local.Sub(midnight)
	return midnight.Add(delta - (delta % step))
}

// SnapEnd returns the smallest grid boundary >= ts with the rounding step
// (TagBlockGranularity) anchored at local midnight. Returns ts unchanged when
// granularity is 0 or ts already sits on the grid.
func (t TrackingConfig) SnapEnd(ts time.Time) time.Time {
	step := t.TagBlockGranularity()
	if step <= 0 {
		return ts
	}
	floor := t.SnapStart(ts)
	if floor.Equal(ts) {
		return ts
	}
	return floor.Add(step)
}

// AppURL returns the Personio web app URL for the configured tenant. Returns
// the empty string when no tenant is configured yet.
func (p PersonioConfig) AppURL() string {
	t := strings.TrimSpace(p.Tenant)
	if t == "" {
		return ""
	}
	return "https://" + t + ".personio.de"
}

// ResolvePaths returns OS-specific paths for data and config.
func ResolvePaths() (Paths, error) {
	cfgRoot, err := userConfigRoot()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve config root: %w", err)
	}
	dataRoot, err := userDataRoot()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve data root: %w", err)
	}
	cfgDir := filepath.Join(cfgRoot, "TimeTracker")
	dataDir := filepath.Join(dataRoot, "TimeTracker")
	return Paths{
		ConfigFile: filepath.Join(cfgDir, "config.toml"),
		DataDir:    dataDir,
		DBFile:     filepath.Join(dataDir, "data.db"),
		LogDir:     filepath.Join(dataDir, "log"),
		AuthDir:    filepath.Join(dataDir, "auth"),
		PluginsDir: filepath.Join(cfgDir, "plugins"),
	}, nil
}

// Load reads the config from path, falling back to defaults if the file does
// not exist. The returned config is always validated.
func Load(path string) (*Config, error) {
	cfg := Default()
	if path == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(path) // #nosec G304 -- path is resolved from OS user dirs.
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if err := Save(path, cfg); err != nil {
				return nil, fmt.Errorf("seed default config: %w", err)
			}
			return cfg, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}
	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	cfg.Communication.ProcessNames = NormalizeProcessNames(cfg.Communication.ProcessNames)
	cfg.Communication.TitleExcludePhrases = NormalizeTitleExcludePhrases(cfg.Communication.TitleExcludePhrases)
	cfg.WorkSchedule.WorkDays = NormalizeWorkDays(cfg.WorkSchedule.WorkDays)
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}
	return cfg, nil
}

// Save persists the config as TOML, creating the parent directory if needed.
func Save(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open config: %w", err)
	}
	defer func() { _ = f.Close() }()
	enc := toml.NewEncoder(f)
	if err := enc.Encode(cfg); err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	return nil
}

var tenantRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)

// guidRe matches the canonical 8-4-4-4-12 hex GUID form used by Entra ID for
// both client (application) and directory (tenant) IDs. We reject the
// well-known meta-tenants "common", "organizations" and "consumers" up the
// stack — Validate() handles that with a clearer error message.
var guidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// NormalizeTenant accepts the variety of inputs a user might paste into the
// settings UI ("example", "example.app.personio.com", "https://example.personio.de/")
// and returns the bare tenant slug ("example"). Returns the trimmed input
// if it already looks like a slug. Returns "" for empty/whitespace input.
func NormalizeTenant(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	// Strip a scheme so url.Parse below sees a fully-qualified URL.
	if !strings.Contains(s, "://") && (strings.Contains(s, ".personio.") || strings.HasPrefix(s, "//")) {
		s = "https://" + strings.TrimPrefix(s, "//")
	}
	if u, err := urlParse(s); err == nil && u != nil && u.Host != "" {
		s = u.Host
	}
	s = strings.ToLower(s)
	s = strings.TrimSuffix(s, ".personio.de")
	s = strings.TrimSuffix(s, ".personio.com")
	s = strings.TrimSuffix(s, ".app")
	// Anything still containing a dot is a hostname we can't simplify
	// (e.g. "example.other.com" — likely a typo). Leave it as-is so the
	// validator surfaces the problem.
	return s
}

// NormalizeGUID accepts the variety of GUID encodings users paste into UI
// inputs ("{abc...}", "ABC-...", surrounding whitespace) and returns the
// canonical lowercased 8-4-4-4-12 form. Empty / whitespace-only input
// returns "". Inputs that don't look like a GUID at all are returned
// trimmed-only so the validator surfaces the problem with the original
// shape intact.
func NormalizeGUID(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	s = strings.TrimPrefix(s, "{")
	s = strings.TrimSuffix(s, "}")
	if guidRe.MatchString(s) {
		return strings.ToLower(s)
	}
	return s
}

// Validate checks the config for invalid combinations and ranges. It returns a
// composite error with all violations.
func (c *Config) Validate() error {
	var errs []string
	if c.Tracking.PollIntervalSec < 1 || c.Tracking.PollIntervalSec > 300 {
		errs = append(errs, "tracking.poll_interval_sec must be in [1,300]")
	}
	if c.Tracking.IdleThresholdMin < 1 || c.Tracking.IdleThresholdMin > 240 {
		errs = append(errs, "tracking.idle_threshold_min must be in [1,240]")
	}
	if c.Tracking.TagBlockGranularityMin < 0 || c.Tracking.TagBlockGranularityMin > 60 {
		errs = append(errs, "tracking.tag_block_granularity_min must be in [0,60] (0 = aus)")
	}
	if t := strings.TrimSpace(c.Personio.Tenant); t != "" {
		if !tenantRe.MatchString(strings.ToLower(t)) {
			errs = append(errs,
				"personio.tenant erwartet den Subdomain-Slug (z. B. \"example\"), nicht eine vollständige URL — bekommen: "+t)
		}
	}
	// Entra ID: client_id and tenant_id are either both empty (feature
	// disabled) or both present and well-formed. We do *not* allow the
	// meta-tenants common/organizations/consumers — single-tenant only.
	cid := strings.TrimSpace(c.Entra.ClientID)
	tid := strings.TrimSpace(c.Entra.TenantID)
	switch {
	case cid == "" && tid == "":
		// feature disabled — nothing to validate.
	case cid == "" || tid == "":
		errs = append(errs, "entra.client_id und entra.tenant_id müssen entweder beide leer oder beide gesetzt sein")
	default:
		if !guidRe.MatchString(cid) {
			errs = append(errs, "entra.client_id erwartet eine GUID im 8-4-4-4-12-Format — bekommen: "+cid)
		}
		switch strings.ToLower(tid) {
		case "common", "organizations", "consumers":
			errs = append(errs, "entra.tenant_id muss eine konkrete Directory-GUID sein, nicht \""+tid+"\" (Multi-Tenant ist nicht unterstützt)")
		default:
			if !guidRe.MatchString(tid) {
				errs = append(errs, "entra.tenant_id erwartet eine GUID im 8-4-4-4-12-Format — bekommen: "+tid)
			}
		}
	}
	if c.QuickTag.Enabled {
		if _, err := ParseHotkey(c.QuickTag.Hotkey); err != nil {
			errs = append(errs, "quick_tag.hotkey ungültig: "+err.Error())
		}
	}
	// Work schedule: hours in [0,24], start strictly before end (an empty or
	// inverted window has no meaningful interpretation). Unknown weekday
	// strings are reported individually so the user can spot a typo;
	// NormalizeWorkDays would otherwise silently drop them.
	if c.WorkSchedule.StartHour < 0 || c.WorkSchedule.StartHour > 23 {
		errs = append(errs, "work_schedule.start_hour must be in [0,23]")
	}
	if c.WorkSchedule.EndHour < 1 || c.WorkSchedule.EndHour > 24 {
		errs = append(errs, "work_schedule.end_hour must be in [1,24]")
	}
	if c.WorkSchedule.StartHour >= 0 && c.WorkSchedule.EndHour <= 24 &&
		c.WorkSchedule.StartHour >= c.WorkSchedule.EndHour {
		errs = append(errs, "work_schedule.start_hour must be strictly less than work_schedule.end_hour")
	}
	for _, d := range c.WorkSchedule.WorkDays {
		if canonicalWorkDay(d) == "" {
			errs = append(errs, "work_schedule.work_days enthält unbekannten Wochentag: "+d)
		}
	}
	if len(errs) == 0 {
		return nil
	}
	msg := "invalid config:"
	for _, e := range errs {
		msg += "\n  - " + e
	}
	return errors.New(msg)
}

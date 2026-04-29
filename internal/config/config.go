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
	Tracking TrackingConfig `toml:"tracking" json:"tracking"`
	Personio PersonioConfig `toml:"personio" json:"personio"`
	UI       UIConfig       `toml:"ui"       json:"ui"`
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

// UIConfig holds UI-related preferences.
type UIConfig struct {
	Autostart bool `toml:"autostart" json:"autostart"`
}

// Paths bundles resolved on-disk locations.
type Paths struct {
	ConfigFile string // %APPDATA%\TimeTracker\config.toml
	DataDir    string // %LOCALAPPDATA%\TimeTracker
	DBFile     string // %LOCALAPPDATA%\TimeTracker\data.db
	LogDir     string // %LOCALAPPDATA%\TimeTracker\log
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
	if len(errs) == 0 {
		return nil
	}
	msg := "invalid config:"
	for _, e := range errs {
		msg += "\n  - " + e
	}
	return errors.New(msg)
}

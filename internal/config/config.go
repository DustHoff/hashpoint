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
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

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

// Validate checks the config for invalid combinations and ranges. It returns a
// composite error with all violations.
func (c *Config) Validate() error {
	var errs []string
	if c.Tracking.PollIntervalSec < 1 || c.Tracking.PollIntervalSec > 30 {
		errs = append(errs, "tracking.poll_interval_sec must be in [1,30]")
	}
	if c.Tracking.IdleThresholdMin < 1 || c.Tracking.IdleThresholdMin > 240 {
		errs = append(errs, "tracking.idle_threshold_min must be in [1,240]")
	}
	if t := strings.TrimSpace(c.Personio.Tenant); t != "" {
		// Empty is allowed (user has not yet configured Personio); but if set,
		// it must look like a subdomain label.
		if !tenantRe.MatchString(strings.ToLower(t)) {
			errs = append(errs, "personio.tenant must be a Personio subdomain (a-z, 0-9, hyphen)")
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

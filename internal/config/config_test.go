package config

import (
	"path/filepath"
	"testing"
)

func TestDefault_PassesValidation(t *testing.T) {
	t.Parallel()
	c := Default()
	if err := c.Validate(); err != nil {
		t.Fatalf("default config invalid: %v", err)
	}
}

func TestValidate_RejectsOutOfRange(t *testing.T) {
	t.Parallel()
	c := Default()
	c.Tracking.PollIntervalSec = 0
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for poll_interval=0")
	}
	c = Default()
	c.Tracking.IdleThresholdMin = 9999
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for huge idle threshold")
	}
	c = Default()
	c.Personio.Tenant = "Has Spaces"
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for invalid tenant subdomain")
	}
}

func TestValidate_AcceptsEmptyTenant(t *testing.T) {
	t.Parallel()
	c := Default()
	c.Personio.Tenant = ""
	if err := c.Validate(); err != nil {
		t.Fatalf("expected empty tenant to be valid (pre-onboarding); got %v", err)
	}
}

func TestNormalizeTenant(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"  example  ", "example"},
		{"EXAMPLE", "example"},
		{"example.personio.de", "example"},
		{"example.app.personio.com", "example"},
		{"https://example.app.personio.com/", "example"},
		{"https://example.personio.de/dashboard", "example"},
		{"http://EXAMPLE.PERSONIO.DE", "example"},
		// Unrecognised host: keep verbatim so the validator surfaces it.
		{"example.other.com", "example.other.com"},
	}
	for _, tc := range cases {
		if got := NormalizeTenant(tc.in); got != tc.want {
			t.Errorf("NormalizeTenant(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestPersonio_AppURL(t *testing.T) {
	t.Parallel()
	c := Default()
	if got := c.Personio.AppURL(); got != "" {
		t.Errorf("expected empty AppURL when tenant unset; got %q", got)
	}
	c.Personio.Tenant = "onesi"
	if got, want := c.Personio.AppURL(), "https://onesi.personio.de"; got != want {
		t.Errorf("AppURL=%q want %q", got, want)
	}
}

func TestLoad_SeedsDefaultsWhenMissing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	c, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.Tracking.PollIntervalSec != 2 {
		t.Errorf("default not seeded; got %d", c.Tracking.PollIntervalSec)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	c := Default()
	c.Personio.Tenant = "onesi"
	if err := Save(p, c); err != nil {
		t.Fatalf("save: %v", err)
	}
	c2, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c2.Personio.Tenant != "onesi" {
		t.Errorf("round-trip lost data: %+v", c2.Personio)
	}
}

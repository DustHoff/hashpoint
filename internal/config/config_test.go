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
	c.Personio.BaseURL = ""
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for empty base url")
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
	c.Personio.ClientID = "abc"
	c.Personio.EmployeeID = "42"
	if err := Save(p, c); err != nil {
		t.Fatalf("save: %v", err)
	}
	c2, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c2.Personio.ClientID != "abc" || c2.Personio.EmployeeID != "42" {
		t.Errorf("round-trip lost data: %+v", c2.Personio)
	}
}

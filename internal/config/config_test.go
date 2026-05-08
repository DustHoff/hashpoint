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

func TestValidate_Entra(t *testing.T) {
	t.Parallel()
	const goodGUID = "11111111-2222-3333-4444-555555555555"
	const otherGUID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

	cases := []struct {
		name      string
		client    string
		tenant    string
		wantValid bool
	}{
		{"both empty (feature off)", "", "", true},
		{"only client_id set", goodGUID, "", false},
		{"only tenant_id set", "", goodGUID, false},
		{"both well-formed", goodGUID, otherGUID, true},
		{"meta-tenant common rejected", goodGUID, "common", false},
		{"meta-tenant organizations rejected", goodGUID, "organizations", false},
		{"non-guid client", "not-a-guid", goodGUID, false},
		{"non-guid tenant", goodGUID, "not-a-guid", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			c.Entra.ClientID = tc.client
			c.Entra.TenantID = tc.tenant
			err := c.Validate()
			if tc.wantValid && err != nil {
				t.Fatalf("want valid, got %v", err)
			}
			if !tc.wantValid && err == nil {
				t.Fatal("want validation error, got nil")
			}
		})
	}
}

func TestEntra_Authority(t *testing.T) {
	t.Parallel()
	c := Default()
	if got := c.Entra.Authority(); got != "" {
		t.Errorf("expected empty Authority when tenant unset; got %q", got)
	}
	c.Entra.TenantID = "11111111-2222-3333-4444-555555555555"
	if got, want := c.Entra.Authority(), "https://login.microsoftonline.com/11111111-2222-3333-4444-555555555555"; got != want {
		t.Errorf("Authority=%q want %q", got, want)
	}
}

func TestEntra_Configured(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		c    EntraConfig
		want bool
	}{
		{"empty", EntraConfig{}, false},
		{"only client", EntraConfig{ClientID: "x"}, false},
		{"only tenant", EntraConfig{TenantID: "x"}, false},
		{"both whitespace", EntraConfig{ClientID: "  ", TenantID: "  "}, false},
		{"both filled", EntraConfig{ClientID: "x", TenantID: "y"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.c.Configured(); got != tc.want {
				t.Errorf("Configured()=%v want %v", got, tc.want)
			}
		})
	}
}

func TestNormalizeGUID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"  ", ""},
		{"11111111-2222-3333-4444-555555555555", "11111111-2222-3333-4444-555555555555"},
		{"  {ABCDEF12-3456-7890-ABCD-EF1234567890}  ", "abcdef12-3456-7890-abcd-ef1234567890"},
		{"not-a-guid", "not-a-guid"}, // pass-through so validator sees the bad input
	}
	for _, tc := range cases {
		if got := NormalizeGUID(tc.in); got != tc.want {
			t.Errorf("NormalizeGUID(%q) = %q, want %q", tc.in, got, tc.want)
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

func TestNormalizeProcessNames(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   []string
		want []string
	}{
		{nil, nil},
		{[]string{}, nil},
		{[]string{"  Teams.exe  "}, []string{"teams.exe"}},
		{[]string{"Teams.exe", "teams.exe"}, []string{"teams.exe"}},
		{[]string{"", "  "}, nil},
		{[]string{"Teams.exe", "Zoom.exe", "slack.exe"}, []string{"teams.exe", "zoom.exe", "slack.exe"}},
	}
	for _, tc := range cases {
		got := NormalizeProcessNames(tc.in)
		if len(got) != len(tc.want) {
			t.Errorf("NormalizeProcessNames(%v) length=%d, want %d (%v)", tc.in, len(got), len(tc.want), got)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("NormalizeProcessNames(%v)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
			}
		}
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	c := Default()
	c.Personio.Tenant = "onesi"
	c.Communication.TitleExcludePhrases = []string{"Benachrichtigung", "Reminder"}
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
	if got, want := c2.Communication.TitleExcludePhrases, []string{"Benachrichtigung", "Reminder"}; !equalStrings(got, want) {
		t.Errorf("title_exclude_phrases round-trip = %v, want %v", got, want)
	}
}

func TestNormalizeTitleExcludePhrases(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"nil", nil, nil},
		{"empty slice", []string{}, nil},
		{"trim only", []string{"  Benachrichtigung  "}, []string{"Benachrichtigung"}},
		{"drops empty / whitespace", []string{"", "   ", "Notification"}, []string{"Notification"}},
		{
			"dedup case-insensitive, keeps first casing",
			[]string{"Notification", "notification", "NOTIFICATION"},
			[]string{"Notification"},
		},
		{
			"preserves order and original casing",
			[]string{"Reminder", "Benachrichtigung", "Stand-by"},
			[]string{"Reminder", "Benachrichtigung", "Stand-by"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NormalizeTitleExcludePhrases(tc.in)
			if !equalStrings(got, tc.want) {
				t.Errorf("NormalizeTitleExcludePhrases(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

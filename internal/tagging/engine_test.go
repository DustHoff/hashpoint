package tagging

import (
	"testing"

	"github.com/dusthoff/hashpoint/internal/storage"
)

func TestCompiledRule_Match(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		rule        storage.Rule
		processName string
		title       string
		want        bool
	}{
		{
			name: "contains process name (case-insensitive)",
			rule: storage.Rule{
				MatchField: storage.MatchProcessName,
				MatchType:  storage.MatchContains,
				Pattern:    "Code",
			},
			processName: "code.exe",
			title:       "main.go",
			want:        true,
		},
		{
			name: "equals title (case-insensitive)",
			rule: storage.Rule{
				MatchField: storage.MatchWindowTitle,
				MatchType:  storage.MatchEquals,
				Pattern:    "Inbox - Outlook",
			},
			processName: "outlook.exe",
			title:       "inbox - outlook",
			want:        true,
		},
		{
			name: "regex on title",
			rule: storage.Rule{
				MatchField: storage.MatchWindowTitle,
				MatchType:  storage.MatchRegex,
				Pattern:    `\bGitHub\b`,
			},
			processName: "chrome.exe",
			title:       "Pull request — GitHub",
			want:        true,
		},
		{
			name: "both required, only one matches",
			rule: storage.Rule{
				MatchField: storage.MatchBoth,
				MatchType:  storage.MatchContains,
				Pattern:    "slack",
			},
			processName: "slack.exe",
			title:       "general",
			want:        false,
		},
		{
			name: "both required, both match",
			rule: storage.Rule{
				MatchField: storage.MatchBoth,
				MatchType:  storage.MatchContains,
				Pattern:    "slack",
			},
			processName: "slack.exe",
			title:       "Slack | acme",
			want:        true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			compiled, err := Compile([]storage.Rule{tc.rule})
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			got := compiled[0].Match(tc.processName, tc.title)
			if got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestFirstMatch_PriorityOrder(t *testing.T) {
	t.Parallel()
	rules := []storage.Rule{
		{ID: 1, MatchField: storage.MatchProcessName, MatchType: storage.MatchContains, Pattern: "chrome", TagID: 10, Priority: 5, Enabled: true},
		{ID: 2, MatchField: storage.MatchProcessName, MatchType: storage.MatchContains, Pattern: "chrome", TagID: 20, Priority: 1, Enabled: true},
	}
	compiled, err := Compile(rules)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	hit := FirstMatch(compiled, "chrome.exe", "GitHub")
	if hit == nil {
		t.Fatal("expected match")
	}
	if hit.Rule.TagID != 10 {
		t.Fatalf("expected priority 5 (TagID 10), got TagID %d", hit.Rule.TagID)
	}
}

func TestValidatePattern(t *testing.T) {
	t.Parallel()
	if err := ValidatePattern(storage.MatchRegex, "[invalid"); err == nil {
		t.Fatal("expected error for invalid regex")
	}
	if err := ValidatePattern(storage.MatchRegex, "(valid|pattern)"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := ValidatePattern(storage.MatchContains, ""); err == nil {
		t.Fatal("expected error for empty pattern")
	}
}

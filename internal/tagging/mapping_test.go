package tagging

import (
	"testing"

	"github.com/dusthoff/hashpoint/internal/storage"
)

func ptr[T any](v T) *T { return &v }

func TestResolve_SubInheritsParent(t *testing.T) {
	t.Parallel()
	parent := storage.Tag{
		ID:                 1,
		Name:               "#projekta",
		PersonioProjectID:  ptr("PRJ-1"),
		PersonioActivityID: ptr("ACT-1"),
		SyncToPersonio:     true,
	}
	sub := storage.Tag{
		ID:          2,
		ParentID:    ptr(int64(1)),
		Name:        "#frontend",
		Description: ptr("Refactoring Login-Flow"),
	}
	tags := map[int64]storage.Tag{1: parent, 2: sub}
	m := Resolve(sub, tags)

	if m.ParentName != "#projekta" {
		t.Errorf("ParentName = %q", m.ParentName)
	}
	if m.SubName != "#frontend" {
		t.Errorf("SubName = %q", m.SubName)
	}
	if m.ProjectID != "PRJ-1" {
		t.Errorf("ProjectID inheritance failed: got %q", m.ProjectID)
	}
	if m.ActivityID != "ACT-1" {
		t.Errorf("ActivityID inheritance failed: got %q", m.ActivityID)
	}
}

func TestResolve_SubOverridesParent(t *testing.T) {
	t.Parallel()
	parent := storage.Tag{
		ID:                 1,
		Name:               "#projekta",
		PersonioProjectID:  ptr("PRJ-1"),
		PersonioActivityID: ptr("ACT-1"),
	}
	sub := storage.Tag{
		ID:                2,
		ParentID:          ptr(int64(1)),
		Name:              "#frontend",
		PersonioProjectID: ptr("PRJ-2"),
	}
	tags := map[int64]storage.Tag{1: parent, 2: sub}
	m := Resolve(sub, tags)
	if m.ProjectID != "PRJ-2" {
		t.Errorf("override failed: got %q", m.ProjectID)
	}
	if m.ActivityID != "ACT-1" {
		t.Errorf("activity should still inherit: got %q", m.ActivityID)
	}
}

func TestBuildComment(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		m    EffectiveMapping
		want string
	}{
		{
			"parent + sub + description",
			EffectiveMapping{ParentName: "#projekta", SubName: "#frontend", SubDescription: "Refactoring Login-Flow"},
			"#projekta #frontend Refactoring Login-Flow",
		},
		{
			"parent + sub no description",
			EffectiveMapping{ParentName: "#projekta", SubName: "#frontend"},
			"#projekta #frontend",
		},
		{
			"parent only",
			EffectiveMapping{ParentName: "#projekta"},
			"#projekta",
		},
		{
			"sub without parent (orphan)",
			EffectiveMapping{SubName: "#frontend", SubDescription: "x"},
			"#frontend x",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.m.BuildComment()
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestParseComment(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		comment string
		parent  string
		sub     string
		desc    string
	}{
		{"full schema em-dash", "#projekta #frontend — Login-Flow fix", "#projekta", "#frontend", "Login-Flow fix"},
		{"full schema hyphen", "#projekta #frontend - Login-Flow fix", "#projekta", "#frontend", "Login-Flow fix"},
		{"parent + sub no description", "#projekta #frontend", "#projekta", "#frontend", ""},
		{"parent + sub trailing separator only", "#projekta #frontend — ", "#projekta", "#frontend", ""},
		{"parent only with description", "#projekta — standup", "#projekta", "", "standup"},
		{"parent only", "#projekta", "#projekta", "", ""},
		{"parent + free text without separator", "#projekta did some work", "#projekta", "", "did some work"},
		{"no hashtag free text", "Meeting mit Team", "", "", "Meeting mit Team"},
		{"empty", "", "", "", ""},
		{"whitespace only", "   \t ", "", "", ""},
		{"three hashtags keep first two, rest into desc", "#a #b #c — d", "#a", "#b", "#c — d"},
		{"description with internal hyphen preserved", "#a #b — re-tag the login-flow", "#a", "#b", "re-tag the login-flow"},
		{"leading/trailing whitespace trimmed", "  #a #b — text  ", "#a", "#b", "text"},
		{"invalid hashtag token is not a tag", "#a, some note", "", "", "#a, some note"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parent, sub, desc := ParseComment(tc.comment)
			if parent != tc.parent || sub != tc.sub || desc != tc.desc {
				t.Fatalf("ParseComment(%q) = (%q, %q, %q), want (%q, %q, %q)",
					tc.comment, parent, sub, desc, tc.parent, tc.sub, tc.desc)
			}
		})
	}
}

func TestParseComment_RoundTripsBuildComment(t *testing.T) {
	t.Parallel()
	// A comment built from parent + sub plus a block description round-trips:
	// the schema tags parse back out and the block description survives. The
	// ` — ` block-description separator is emitted by personio.buildComment
	// (not BuildComment), so we assemble it here.
	m := EffectiveMapping{ParentName: "#projekta", SubName: "#frontend"}
	comment := m.BuildComment() + " — Refactoring"
	parent, sub, desc := ParseComment(comment)
	if parent != "#projekta" || sub != "#frontend" || desc != "Refactoring" {
		t.Fatalf("round-trip = (%q, %q, %q)", parent, sub, desc)
	}
}

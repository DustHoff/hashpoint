package tagging

import (
	"testing"

	"github.com/onesi/hashpoint/internal/storage"
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

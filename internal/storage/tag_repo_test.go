package storage

import (
	"context"
	"errors"
	"testing"
)

func ptr[T any](v T) *T { return &v }

func TestTagRepo_HierarchyAndUniqueness(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	repo := NewTagRepo(db)

	parent := &Tag{Name: "#projekta", SyncToPersonio: true}
	if err := repo.Create(ctx, parent); err != nil {
		t.Fatalf("create parent: %v", err)
	}

	sub := &Tag{Name: "#frontend", ParentID: ptr(parent.ID), SyncToPersonio: true}
	if err := repo.Create(ctx, sub); err != nil {
		t.Fatalf("create sub: %v", err)
	}

	dup := &Tag{Name: "#frontend", ParentID: ptr(parent.ID), SyncToPersonio: true}
	if err := repo.Create(ctx, dup); err == nil {
		t.Fatal("expected uniqueness violation")
	}

	children, err := repo.Children(ctx, parent.ID)
	if err != nil {
		t.Fatalf("children: %v", err)
	}
	if len(children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(children))
	}
}

func TestTagRepo_NameCheckRejectsInvalid(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	repo := NewTagRepo(db)

	bad := &Tag{Name: "no-hash"}
	if err := repo.Create(ctx, bad); err == nil {
		t.Fatal("expected CHECK constraint failure")
	}
}

func TestTagRepo_EnsureByPath_CreatesMissingHierarchy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	repo := NewTagRepo(db)

	leaf, err := repo.EnsureByPath(ctx, "productivity/coding")
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if leaf.Name != "#coding" {
		t.Errorf("leaf name = %q, want %q", leaf.Name, "#coding")
	}
	if leaf.ParentID == nil {
		t.Fatalf("leaf has no parent — hierarchy missing")
	}
	parent, err := repo.Get(ctx, *leaf.ParentID)
	if err != nil {
		t.Fatalf("get parent: %v", err)
	}
	if parent.Name != "#productivity" {
		t.Errorf("parent name = %q, want %q", parent.Name, "#productivity")
	}
	if parent.ParentID != nil {
		t.Errorf("parent should be root, got parent_id=%v", *parent.ParentID)
	}
}

func TestTagRepo_EnsureByPath_ReturnsExistingTagsCaseInsensitive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	repo := NewTagRepo(db)

	// Seed with mixed-case names — second call must reuse, not duplicate.
	first, err := repo.EnsureByPath(ctx, "Productivity/Coding")
	if err != nil {
		t.Fatalf("first ensure: %v", err)
	}
	second, err := repo.EnsureByPath(ctx, "productivity/coding")
	if err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("expected reuse, got distinct IDs %d / %d", first.ID, second.ID)
	}

	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("expected 2 tags total, got %d", len(all))
	}
}

func TestTagRepo_EnsureByPath_NormalizesAndStripsLeadingHash(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	repo := NewTagRepo(db)

	leaf, err := repo.EnsureByPath(ctx, " #Project A / sub-tag! ")
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if leaf.Name != "#subtag" {
		t.Errorf("leaf name = %q, want %q", leaf.Name, "#subtag")
	}
	if leaf.ParentID == nil {
		t.Fatalf("leaf has no parent")
	}
	parent, err := repo.Get(ctx, *leaf.ParentID)
	if err != nil {
		t.Fatalf("get parent: %v", err)
	}
	if parent.Name != "#ProjectA" {
		t.Errorf("parent name = %q, want %q", parent.Name, "#ProjectA")
	}
}

func TestTagRepo_EnsureByPath_RejectsEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	repo := NewTagRepo(db)

	cases := []string{"", "/", "  ", "#", "/// ", "!@#$%"}
	for _, in := range cases {
		if _, err := repo.EnsureByPath(ctx, in); !errors.Is(err, ErrInvalidTagPath) {
			t.Errorf("EnsureByPath(%q) error = %v, want ErrInvalidTagPath", in, err)
		}
	}
}

func TestTagRepo_EnsureByPathWithMetadata_FirstCreateAppliesMeta(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	repo := NewTagRepo(db)

	leaf, created, err := repo.EnsureByPathWithMetadata(ctx, "jira/PROJ-123", TagMetadata{
		Description: "Customer onboarding",
		Color:       "#7c3aed",
	})
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if !created {
		t.Fatal("expected createdLeaf=true on first import")
	}
	if leaf.Description == nil || *leaf.Description != "Customer onboarding" {
		t.Errorf("description = %v, want %q", leaf.Description, "Customer onboarding")
	}
	if leaf.Color == nil || *leaf.Color != "#7c3aed" {
		t.Errorf("color = %v, want %q", leaf.Color, "#7c3aed")
	}
}

func TestTagRepo_EnsureByPathWithMetadata_ExistingLeafNotModified(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	repo := NewTagRepo(db)

	// User manually creates the leaf with their own description first.
	parent := &Tag{Name: "#jira"}
	if err := repo.Create(ctx, parent); err != nil {
		t.Fatalf("create parent: %v", err)
	}
	userLeaf := &Tag{
		Name:        "#proj123",
		ParentID:    ptr(parent.ID),
		Description: ptr("User-written notes"),
		Color:       ptr("#ff0000"),
	}
	if err := repo.Create(ctx, userLeaf); err != nil {
		t.Fatalf("create user leaf: %v", err)
	}

	// Plugin imports the same path with different metadata.
	leaf, created, err := repo.EnsureByPathWithMetadata(ctx, "jira/PROJ-123", TagMetadata{
		Description: "Plugin description",
		Color:       "#00ff00",
	})
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if created {
		t.Fatal("expected createdLeaf=false when leaf already exists")
	}
	if leaf.Description == nil || *leaf.Description != "User-written notes" {
		t.Errorf("user description was overwritten: got %v", leaf.Description)
	}
	if leaf.Color == nil || *leaf.Color != "#ff0000" {
		t.Errorf("user color was overwritten: got %v", leaf.Color)
	}
	if leaf.ID != userLeaf.ID {
		t.Errorf("leaf id changed (got %d, want %d) — should reuse the existing row", leaf.ID, userLeaf.ID)
	}
}

func TestTagRepo_EnsureByPathWithMetadata_IntermediateNodesStayBare(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	repo := NewTagRepo(db)

	leaf, _, err := repo.EnsureByPathWithMetadata(ctx, "jira/PROJ-1/task-A", TagMetadata{
		Description: "leaf only",
		Color:       "#abcdef",
	})
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if leaf.Description == nil || *leaf.Description != "leaf only" {
		t.Errorf("leaf description = %v, want leaf only", leaf.Description)
	}
	// Walk up: the two intermediate nodes (jira, proj1) should have no
	// description / color — metadata is leaf-only.
	cur := leaf
	for cur.ParentID != nil {
		p, err := repo.Get(ctx, *cur.ParentID)
		if err != nil || p == nil {
			t.Fatalf("get parent: %v", err)
		}
		if p.Description != nil {
			t.Errorf("intermediate %q got a description (%q) — metadata must stay leaf-only", p.Name, *p.Description)
		}
		if p.Color != nil {
			t.Errorf("intermediate %q got a color (%q) — metadata must stay leaf-only", p.Name, *p.Color)
		}
		cur = p
	}
}

func TestTagRepo_EnsureByPathWithMetadata_SecondCallIsNoop(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	repo := NewTagRepo(db)

	_, created1, err := repo.EnsureByPathWithMetadata(ctx, "jira/PROJ-9", TagMetadata{
		Description: "first",
	})
	if err != nil {
		t.Fatalf("ensure 1: %v", err)
	}
	if !created1 {
		t.Fatal("first call should report created=true")
	}
	leaf2, created2, err := repo.EnsureByPathWithMetadata(ctx, "jira/PROJ-9", TagMetadata{
		Description: "second — must be ignored",
	})
	if err != nil {
		t.Fatalf("ensure 2: %v", err)
	}
	if created2 {
		t.Fatal("second call should report created=false")
	}
	if leaf2.Description == nil || *leaf2.Description != "first" {
		t.Errorf("description changed on the second call: got %v, want %q", leaf2.Description, "first")
	}
}

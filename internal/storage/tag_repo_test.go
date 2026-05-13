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

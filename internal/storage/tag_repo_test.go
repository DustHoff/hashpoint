package storage

import (
	"context"
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

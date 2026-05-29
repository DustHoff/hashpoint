package storage

import (
	"context"
	"testing"
)

func TestPluginApprovalRepo_RoundTrip(t *testing.T) {
	ctx := context.Background()
	db, err := OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := NewPluginApprovalRepo(NewSettingsRepo(db))

	ok, err := repo.IsApproved(ctx, "alpha")
	if err != nil {
		t.Fatalf("IsApproved: %v", err)
	}
	if ok {
		t.Error("alpha should not be approved initially")
	}

	if err := repo.Approve(ctx, "alpha"); err != nil {
		t.Fatalf("Approve alpha: %v", err)
	}
	// Idempotent: approving again must not duplicate or error.
	if err := repo.Approve(ctx, "alpha"); err != nil {
		t.Fatalf("Approve alpha (again): %v", err)
	}
	if err := repo.Approve(ctx, "beta"); err != nil {
		t.Fatalf("Approve beta: %v", err)
	}

	ok, err = repo.IsApproved(ctx, "alpha")
	if err != nil {
		t.Fatalf("IsApproved alpha: %v", err)
	}
	if !ok {
		t.Error("alpha should be approved after Approve")
	}

	list, err := repo.ListApproved(ctx)
	if err != nil {
		t.Fatalf("ListApproved: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("expected exactly 2 approved (alpha, beta), got %v", list)
	}

	// A fresh repo over the same DB must see the persisted set.
	repo2 := NewPluginApprovalRepo(NewSettingsRepo(db))
	ok, err = repo2.IsApproved(ctx, "beta")
	if err != nil || !ok {
		t.Errorf("persisted approval not visible to a new repo: ok=%v err=%v", ok, err)
	}
}

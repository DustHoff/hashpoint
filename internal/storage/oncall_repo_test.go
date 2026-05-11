package storage

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestOnCallRepo_EnsureForBlockIdempotent(t *testing.T) {
	ctx := context.Background()
	db, err := OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	tagRepo := NewTagRepo(db)
	tg := &Tag{Name: "#oncall", SyncToPersonio: true}
	if err := tagRepo.Create(ctx, tg); err != nil {
		t.Fatalf("create tag: %v", err)
	}
	blockRepo := NewTagBlockRepo(db)
	end := time.Date(2026, 5, 4, 22, 0, 0, 0, time.UTC)
	b := &TagBlock{TagID: tg.ID, StartTime: end.Add(-2 * time.Hour), EndTime: &end, IsManual: true}
	if err := blockRepo.Open(ctx, b); err != nil {
		t.Fatalf("open block: %v", err)
	}

	repo := NewOnCallRepo(db)
	first, err := repo.EnsureForBlock(ctx, b.ID, tg.ID)
	if err != nil {
		t.Fatalf("ensure first: %v", err)
	}
	if first.BlockID != b.ID || first.TagAtCreation != tg.ID {
		t.Fatalf("first: unexpected fields: %+v", first)
	}
	if got := first.Status(); got != OnCallStatusDraft {
		t.Fatalf("status: want draft, got %q", got)
	}

	second, err := repo.EnsureForBlock(ctx, b.ID, 999) // wrong tag — must be ignored
	if err != nil {
		t.Fatalf("ensure second: %v", err)
	}
	if second.ID != first.ID || second.TagAtCreation != tg.ID {
		t.Fatalf("re-ensure changed row: %+v", second)
	}
}

func TestOnCallRepo_UpdateDraftAndStaleFlag(t *testing.T) {
	ctx := context.Background()
	db, err := OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	tagRepo := NewTagRepo(db)
	tg := &Tag{Name: "#oncall", SyncToPersonio: true}
	if err := tagRepo.Create(ctx, tg); err != nil {
		t.Fatalf("create tag: %v", err)
	}
	blockRepo := NewTagBlockRepo(db)
	end := time.Date(2026, 5, 4, 22, 0, 0, 0, time.UTC)
	b := &TagBlock{TagID: tg.ID, StartTime: end.Add(-2 * time.Hour), EndTime: &end, IsManual: true}
	if err := blockRepo.Open(ctx, b); err != nil {
		t.Fatalf("open block: %v", err)
	}

	repo := NewOnCallRepo(db)
	doc, err := repo.EnsureForBlock(ctx, b.ID, tg.ID)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}

	if err := repo.UpdateDraft(ctx, doc.ID, "billing-api", OnCallIncidentServiceDisruption, "Restarted pod; cause TBD."); err != nil {
		t.Fatalf("update draft: %v", err)
	}
	got, err := repo.Get(ctx, doc.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Application != "billing-api" || got.IncidentType != OnCallIncidentServiceDisruption || got.Solution == "" {
		t.Fatalf("draft round-trip: %+v", got)
	}

	if err := repo.MarkStale(ctx, doc.ID); err != nil {
		t.Fatalf("mark stale: %v", err)
	}
	got, _ = repo.Get(ctx, doc.ID)
	if !got.Stale {
		t.Fatalf("stale flag did not set")
	}
	if err := repo.ClearStale(ctx, doc.ID); err != nil {
		t.Fatalf("clear stale: %v", err)
	}
	got, _ = repo.Get(ctx, doc.ID)
	if got.Stale {
		t.Fatalf("stale flag did not clear")
	}
}

func TestOnCallRepo_SubmissionStatusRollup(t *testing.T) {
	ctx := context.Background()
	db, err := OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	tagRepo := NewTagRepo(db)
	tg := &Tag{Name: "#oncall", SyncToPersonio: true}
	if err := tagRepo.Create(ctx, tg); err != nil {
		t.Fatalf("create tag: %v", err)
	}
	blockRepo := NewTagBlockRepo(db)
	end := time.Date(2026, 5, 4, 22, 0, 0, 0, time.UTC)
	b := &TagBlock{TagID: tg.ID, StartTime: end.Add(-2 * time.Hour), EndTime: &end, IsManual: true}
	if err := blockRepo.Open(ctx, b); err != nil {
		t.Fatalf("open block: %v", err)
	}

	repo := NewOnCallRepo(db)
	doc, _ := repo.EnsureForBlock(ctx, b.ID, tg.ID)

	jira, err := repo.EnsureSubmission(ctx, doc.ID, "jira")
	if err != nil {
		t.Fatalf("ensure jira: %v", err)
	}
	otrs, err := repo.EnsureSubmission(ctx, doc.ID, "otrs")
	if err != nil {
		t.Fatalf("ensure otrs: %v", err)
	}

	now := time.Now().UTC()
	// Status pending while both are still in flight.
	if d, _ := repo.Get(ctx, doc.ID); d.Status() != OnCallStatusPending {
		t.Fatalf("want pending, got %q", d.Status())
	}

	if err := repo.MarkSubmissionSubmitted(ctx, jira.ID, "JSM-42", "https://x/JSM-42", now); err != nil {
		t.Fatalf("mark jira submitted: %v", err)
	}
	if err := repo.MarkSubmissionFailed(ctx, otrs.ID, "401 Unauthorized", now); err != nil {
		t.Fatalf("mark otrs failed: %v", err)
	}

	d, _ := repo.Get(ctx, doc.ID)
	if got := d.Status(); got != OnCallStatusPartial {
		t.Fatalf("want partial, got %q", got)
	}

	// Retry: only the failed row goes back to pending. Submitted stays submitted.
	if err := repo.MarkSubmissionPending(ctx, otrs.ID); err != nil {
		t.Fatalf("mark otrs pending again: %v", err)
	}
	d, _ = repo.Get(ctx, doc.ID)
	if got := d.Status(); got != OnCallStatusPending {
		t.Fatalf("want pending after retry, got %q", got)
	}
	if err := repo.MarkSubmissionSubmitted(ctx, otrs.ID, "INC-7", "https://otrs/INC-7", now); err != nil {
		t.Fatalf("mark otrs submitted: %v", err)
	}
	d, _ = repo.Get(ctx, doc.ID)
	if got := d.Status(); got != OnCallStatusSubmitted {
		t.Fatalf("want submitted, got %q", got)
	}
}

func TestOnCallRepo_ListAndCascade(t *testing.T) {
	ctx := context.Background()
	db, err := OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	tagRepo := NewTagRepo(db)
	tg := &Tag{Name: "#oncall", SyncToPersonio: true}
	if err := tagRepo.Create(ctx, tg); err != nil {
		t.Fatalf("create tag: %v", err)
	}
	blockRepo := NewTagBlockRepo(db)

	mkBlock := func(start time.Time) int64 {
		end := start.Add(time.Hour)
		b := &TagBlock{TagID: tg.ID, StartTime: start, EndTime: &end, IsManual: true}
		if err := blockRepo.Open(ctx, b); err != nil {
			t.Fatalf("open block: %v", err)
		}
		return b.ID
	}

	now := time.Date(2026, 5, 4, 22, 0, 0, 0, time.UTC)
	older := mkBlock(now.Add(-3 * 24 * time.Hour))
	newer := mkBlock(now.Add(-1 * 24 * time.Hour))

	repo := NewOnCallRepo(db)
	if _, err := repo.EnsureForBlock(ctx, older, tg.ID); err != nil {
		t.Fatalf("ensure older: %v", err)
	}
	if _, err := repo.EnsureForBlock(ctx, newer, tg.ID); err != nil {
		t.Fatalf("ensure newer: %v", err)
	}

	docs, err := repo.List(ctx, OnCallFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(docs) != 2 || docs[0].BlockID != newer || docs[1].BlockID != older {
		t.Fatalf("list ordering wrong: %+v", docs)
	}

	// Deleting the underlying block cascades the doc away.
	if err := blockRepo.Delete(ctx, older); err != nil {
		t.Fatalf("delete block: %v", err)
	}
	if _, err := repo.GetByBlock(ctx, older); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cascade: want ErrNotFound, got %v", err)
	}
}

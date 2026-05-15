package app

import (
	"context"
	"log/slog"
	"testing"

	"github.com/dusthoff/hashpoint/internal/config"
	"github.com/dusthoff/hashpoint/internal/storage"
)

// stubTagRepo implements just enough of storage.TagRepository for the
// pruneOnCallTagIDs test: Delete removes from `present` and List reports
// what is left. All other methods panic so an accidental dependency on
// them shows up loudly.
type stubTagRepo struct {
	present map[int64]storage.Tag
}

func (r *stubTagRepo) Delete(_ context.Context, id int64) error {
	delete(r.present, id)
	return nil
}

func (r *stubTagRepo) List(_ context.Context) ([]storage.Tag, error) {
	out := make([]storage.Tag, 0, len(r.present))
	for _, t := range r.present {
		out = append(out, t)
	}
	return out, nil
}

func (r *stubTagRepo) Create(context.Context, *storage.Tag) error {
	panic("not used")
}
func (r *stubTagRepo) Update(context.Context, *storage.Tag) error {
	panic("not used")
}
func (r *stubTagRepo) Get(_ context.Context, id int64) (*storage.Tag, error) {
	if t, ok := r.present[id]; ok {
		return &t, nil
	}
	return nil, storage.ErrNotFound
}
func (r *stubTagRepo) Children(context.Context, int64) ([]storage.Tag, error) {
	panic("not used")
}
func (r *stubTagRepo) EnsureByPath(context.Context, string) (*storage.Tag, error) {
	panic("not used")
}
func (r *stubTagRepo) EnsureByPathWithMetadata(context.Context, string, storage.TagMetadata) (*storage.Tag, bool, error) {
	panic("not used")
}

func TestDeleteTag_PrunesOnCallTagIDs(t *testing.T) {
	t.Parallel()
	repo := &stubTagRepo{present: map[int64]storage.Tag{
		1: {ID: 1, Name: "alpha"},
		2: {ID: 2, Name: "beta"},
		3: {ID: 3, Name: "gamma"},
	}}
	cfg := config.Default()
	cfg.OnCall.TagIDs = []int64{1, 2, 3}

	app := &App{
		ctx:    context.Background(),
		logger: slog.Default(),
		deps: Deps{
			Tags: repo,
			// ConfigPath empty — pruneOnCallTagIDs skips persistence so we
			// don't need a real TOML file. The in-memory cfg field is the
			// source of truth for the assertion.
		},
		cfg: cfg,
	}

	// Delete tag 2: expect cfg.OnCall.TagIDs == [1, 3]
	if err := app.DeleteTag(2); err != nil {
		t.Fatalf("DeleteTag(2) failed: %v", err)
	}
	got := append([]int64(nil), app.cfg.OnCall.TagIDs...)
	if len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Fatalf("after DeleteTag(2): on_call.tag_ids = %v, want [1 3]", got)
	}

	// Deleting a tag that is not in OnCall.TagIDs leaves the list alone.
	if err := app.DeleteTag(99); err != nil {
		t.Fatalf("DeleteTag(99) failed: %v", err)
	}
	got = append([]int64(nil), app.cfg.OnCall.TagIDs...)
	if len(got) != 2 {
		t.Fatalf("after DeleteTag(99) (no-op): on_call.tag_ids = %v, want [1 3]", got)
	}

	// Delete the last on-call tag → list becomes empty.
	if err := app.DeleteTag(1); err != nil {
		t.Fatalf("DeleteTag(1) failed: %v", err)
	}
	if err := app.DeleteTag(3); err != nil {
		t.Fatalf("DeleteTag(3) failed: %v", err)
	}
	if len(app.cfg.OnCall.TagIDs) != 0 {
		t.Fatalf("after deleting all configured tags: on_call.tag_ids = %v, want []", app.cfg.OnCall.TagIDs)
	}
}

func TestPruneOnCallTagIDs_NoopWhenAllAlive(t *testing.T) {
	t.Parallel()
	repo := &stubTagRepo{present: map[int64]storage.Tag{
		1: {ID: 1, Name: "alpha"},
		2: {ID: 2, Name: "beta"},
	}}
	cfg := config.Default()
	cfg.OnCall.TagIDs = []int64{1, 2}
	prev := append([]int64(nil), cfg.OnCall.TagIDs...)

	app := &App{
		ctx:    context.Background(),
		logger: slog.Default(),
		deps:   Deps{Tags: repo},
		cfg:    cfg,
	}

	if err := app.pruneOnCallTagIDs(app.ctx); err != nil {
		t.Fatalf("pruneOnCallTagIDs: %v", err)
	}
	if len(app.cfg.OnCall.TagIDs) != len(prev) {
		t.Fatalf("no-op prune mutated list: got %v, want %v", app.cfg.OnCall.TagIDs, prev)
	}
}

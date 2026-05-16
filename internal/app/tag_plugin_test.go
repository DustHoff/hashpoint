package app

import (
	"context"
	"errors"
	"testing"

	pluginhost "github.com/dusthoff/hashpoint/internal/plugin"
	"github.com/dusthoff/hashpoint/internal/storage"
	"github.com/dusthoff/hashpoint/plugin/sdk"
)

// recordingTagRepo wraps fakeTagRepo's surface but tracks every
// EnsureByPathWithMetadata call so the sink-merge contract can be
// verified end-to-end.
type recordingTagRepo struct {
	fakeTagRepo
	// onEnsure scripts the response per call. Maps path → (createdLeaf, err).
	// Missing entries return createdLeaf=true with a synthetic Tag.
	onEnsure map[string]struct {
		created bool
		err     error
	}
	calls []recordedEnsureCall
}

type recordedEnsureCall struct {
	path string
	meta storage.TagMetadata
}

func (r *recordingTagRepo) EnsureByPathWithMetadata(_ context.Context, path string, meta storage.TagMetadata) (*storage.Tag, bool, error) {
	r.calls = append(r.calls, recordedEnsureCall{path: path, meta: meta})
	if r.onEnsure != nil {
		if scripted, ok := r.onEnsure[path]; ok {
			if scripted.err != nil {
				return nil, false, scripted.err
			}
			return &storage.Tag{Name: path}, scripted.created, nil
		}
	}
	return &storage.Tag{Name: path}, true, nil
}

func TestAppTagSink_Publish_HappyPath(t *testing.T) {
	t.Parallel()
	repo := &recordingTagRepo{}
	sink := &appTagSink{tags: repo}
	in := []pluginhost.ImportedTagView{
		{Path: "jira/PROJ-1", Description: "First", Color: "#7c3aed", OrderName: "Auftrag-1"},
		{Path: "jira/PROJ-2"},
	}
	created, err := sink.Publish(context.Background(), "jira-plug", in)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if created != 2 {
		t.Errorf("created = %d, want 2 (all new)", created)
	}
	if len(repo.calls) != 2 {
		t.Fatalf("repo calls = %d, want 2", len(repo.calls))
	}
	if repo.calls[0].meta.Description != "First" || repo.calls[0].meta.Color != "#7c3aed" {
		t.Errorf("metadata not propagated: %+v", repo.calls[0].meta)
	}
	if repo.calls[0].meta.OrderName != "Auftrag-1" {
		t.Errorf("OrderName not propagated: got %q, want %q", repo.calls[0].meta.OrderName, "Auftrag-1")
	}
	if repo.calls[1].meta.OrderName != "" {
		t.Errorf("OrderName for unset import = %q, want empty", repo.calls[1].meta.OrderName)
	}
}

func TestAppTagSink_Publish_ExistingPathReportsZero(t *testing.T) {
	t.Parallel()
	repo := &recordingTagRepo{
		onEnsure: map[string]struct {
			created bool
			err     error
		}{
			"existing": {created: false},
			"fresh":    {created: true},
		},
	}
	sink := &appTagSink{tags: repo}
	in := []pluginhost.ImportedTagView{
		{Path: "existing"},
		{Path: "fresh"},
	}
	created, err := sink.Publish(context.Background(), "p", in)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if created != 1 {
		t.Errorf("created = %d, want 1 (one new, one already existing)", created)
	}
}

func TestAppTagSink_Publish_PartialFailureKeepsGoing(t *testing.T) {
	t.Parallel()
	repo := &recordingTagRepo{
		onEnsure: map[string]struct {
			created bool
			err     error
		}{
			"bad":   {err: errors.New("bad path")},
			"good1": {created: true},
			"good2": {created: true},
		},
	}
	sink := &appTagSink{tags: repo}
	in := []pluginhost.ImportedTagView{
		{Path: "good1"},
		{Path: "bad"},
		{Path: "good2"},
	}
	created, err := sink.Publish(context.Background(), "p", in)
	if err == nil {
		t.Fatal("expected the first failure to surface as an error")
	}
	if created != 2 {
		t.Errorf("created = %d, want 2 (the two valid paths)", created)
	}
}

func TestAppTagSink_Publish_NilRepoReturnsError(t *testing.T) {
	t.Parallel()
	sink := &appTagSink{tags: nil}
	_, err := sink.Publish(context.Background(), "p", []pluginhost.ImportedTagView{{Path: "x"}})
	if err == nil {
		t.Fatal("expected error when sink has no repo")
	}
}

// listingTagRepo is a fakeTagRepo extension that lets a test script
// what Tags.List returns. Used by the snapshot-builder tests.
type listingTagRepo struct {
	fakeTagRepo
	rows []storage.Tag
}

func (r *listingTagRepo) List(context.Context) ([]storage.Tag, error) { return r.rows, nil }

func sptr(s string) *string { return &s }
func iptr(i int64) *int64   { return &i }

func TestBuildTagOrderSnapshot_FlatPathsAndOrderNames(t *testing.T) {
	t.Parallel()
	repo := &listingTagRepo{rows: []storage.Tag{
		{ID: 1, Name: "#personio"},
		{ID: 2, ParentID: iptr(1), Name: "#projekta", OrderName: sptr("Auftrag-42")},
		{ID: 3, ParentID: iptr(1), Name: "#projektb"},
		{ID: 4, Name: "#admin", OrderName: sptr("Freitext")},
	}}

	got, err := buildTagOrderSnapshot(context.Background(), repo)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	want := []sdk.TagOrderMapping{
		{TagPath: "admin", OrderName: "Freitext"},
		{TagPath: "personio", OrderName: ""},
		{TagPath: "personio/projekta", OrderName: "Auftrag-42"},
		{TagPath: "personio/projektb", OrderName: ""},
	}
	if len(got) != len(want) {
		t.Fatalf("snapshot len = %d, want %d (%+v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("snapshot[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestBuildTagOrderSnapshot_IncludesEmptyOrderNames(t *testing.T) {
	t.Parallel()
	// Decision: snapshot covers every tag, even those without an
	// order_name — the plugin owns the diff so an empty OrderName must
	// carry the "currently unmapped" signal.
	repo := &listingTagRepo{rows: []storage.Tag{
		{ID: 1, Name: "#solo"},
	}}
	got, err := buildTagOrderSnapshot(context.Background(), repo)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("snapshot len = %d, want 1 (tag without order_name must still appear)", len(got))
	}
	if got[0].OrderName != "" {
		t.Errorf("OrderName = %q, want empty string", got[0].OrderName)
	}
}

func TestBuildTagOrderSnapshot_DanglingParentEmitsPartialPath(t *testing.T) {
	t.Parallel()
	// Parent ID 99 is not in the list — buildTagOrderSnapshot walks
	// what it can and stops, so the leaf still shows up under just its
	// own name. This guards against a silent drop if the FK ever
	// breaks for an in-flight delete.
	repo := &listingTagRepo{rows: []storage.Tag{
		{ID: 1, ParentID: iptr(99), Name: "#orphan", OrderName: sptr("X")},
	}}
	got, err := buildTagOrderSnapshot(context.Background(), repo)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 1 || got[0].TagPath != "orphan" {
		t.Errorf("snapshot = %+v, want single entry with TagPath=%q", got, "orphan")
	}
}

func TestBuildTagOrderSnapshot_EmptyNameDropped(t *testing.T) {
	t.Parallel()
	// A tag whose Name reduces to "" after stripping the # prefix
	// (degenerate but defensive) must not pollute the snapshot.
	repo := &listingTagRepo{rows: []storage.Tag{
		{ID: 1, Name: "#"},
		{ID: 2, Name: "#real", OrderName: sptr("A")},
	}}
	got, err := buildTagOrderSnapshot(context.Background(), repo)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 1 || got[0].TagPath != "real" {
		t.Errorf("snapshot = %+v, want single entry with TagPath=%q", got, "real")
	}
}

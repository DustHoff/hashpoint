package app

import (
	"context"
	"errors"
	"testing"

	pluginhost "github.com/dusthoff/hashpoint/internal/plugin"
	"github.com/dusthoff/hashpoint/internal/storage"
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
		{Path: "jira/PROJ-1", Description: "First", Color: "#7c3aed"},
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

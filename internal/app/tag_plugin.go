package app

import (
	"context"
	"fmt"

	pluginhost "github.com/dusthoff/hashpoint/internal/plugin"
	"github.com/dusthoff/hashpoint/internal/storage"
)

// appTagSource is the App-layer implementation of
// pluginhost.TagSource. It projects the tags repo to TagView so plugins
// never see Personio identifiers or sync flags.
type appTagSource struct {
	tags storage.TagRepository
}

// List returns every tag the repo knows about, in the order the repo
// chooses (parent_id, name). Errors are surfaced verbatim — the bound
// HostAPI logs and wraps.
func (s *appTagSource) List(ctx context.Context) ([]pluginhost.TagView, error) {
	if s == nil || s.tags == nil {
		return nil, nil
	}
	rows, err := s.tags.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tags: %w", err)
	}
	out := make([]pluginhost.TagView, 0, len(rows))
	for _, t := range rows {
		view := pluginhost.TagView{
			ID:   t.ID,
			Name: t.Name,
		}
		if t.ParentID != nil {
			view.ParentID = *t.ParentID
		}
		if t.Color != nil {
			view.Color = *t.Color
		}
		out = append(out, view)
	}
	return out, nil
}

// appTagSink is the App-layer implementation of pluginhost.TagSink. It
// runs each imported path through EnsureByPathWithMetadata — existing
// paths are no-ops (user-tag wins), only previously-unknown leaves are
// created with the plugin's Description / Color.
type appTagSink struct {
	tags storage.TagRepository
}

// Publish merges the supplied tags into the host's tag store. Returns
// the number of leaves the call actually created. A single failing
// entry does not abort the batch — the function logs the failure via
// the returned error wrapping (so the caller can warn the user) but
// continues so a partial import still happens.
//
// pluginName is informational and shows up in the returned error
// when a single entry fails (so the user sees who tried to import
// what).
func (s *appTagSink) Publish(ctx context.Context, pluginName string, tags []pluginhost.ImportedTagView) (int, error) {
	if s == nil || s.tags == nil {
		return 0, fmt.Errorf("tag sink not configured")
	}
	created := 0
	var firstErr error
	for _, t := range tags {
		meta := storage.TagMetadata{
			Description: t.Description,
			Color:       t.Color,
		}
		_, wasCreated, err := s.tags.EnsureByPathWithMetadata(ctx, t.Path, meta)
		if err != nil {
			// Record but keep going — one bad path should not poison
			// the whole import. The first failure is returned so the
			// caller can surface "n created, first error: …".
			if firstErr == nil {
				firstErr = fmt.Errorf("plugin %q tag %q: %w", pluginName, t.Path, err)
			}
			continue
		}
		if wasCreated {
			created++
		}
	}
	return created, firstErr
}

// currentTagSource is the lambda the plugin host's TagSource hook
// invokes on every HostAPI.ListTags call. Returns nil when the App
// was constructed without a tags repo — the bound HostAPI then
// reports an empty slice to the plugin.
func (a *App) currentTagSource() pluginhost.TagSource {
	if a == nil || a.deps.Tags == nil {
		return nil
	}
	return &appTagSource{tags: a.deps.Tags}
}

// currentTagSink is the lambda the plugin host's TagSink hook invokes
// at plugin launch (pull-on-start) and on HostAPI.PublishTags. Returns
// nil when the App was constructed without a tags repo — the bound
// HostAPI then surfaces ErrPublishTagsNotAllowed to plugins that
// expected to publish.
func (a *App) currentTagSink() pluginhost.TagSink {
	if a == nil || a.deps.Tags == nil {
		return nil
	}
	return &appTagSink{tags: a.deps.Tags}
}

// PluginRefreshTags re-pulls the named plugin's tag catalogue and
// merges it into the host store. The Wails frontend wires this to the
// "Tags neu laden" button next to each tag_provider plugin in the
// Plugins tab. Returns the number of newly-created tags so the UI can
// show a meaningful toast (e.g. "5 neue Tags importiert").
func (a *App) PluginRefreshTags(name string) (int, error) {
	if a.pluginHost == nil {
		return 0, fmt.Errorf("plugin host not configured")
	}
	return a.pluginHost.RefreshPluginTags(a.ctx, name)
}

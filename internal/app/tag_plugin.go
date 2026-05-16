package app

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	pluginhost "github.com/dusthoff/hashpoint/internal/plugin"
	"github.com/dusthoff/hashpoint/internal/storage"
	"github.com/dusthoff/hashpoint/plugin/sdk"
)

// notifyTagOrdersTimeout caps the snapshot build (Tags.List + path
// walk) before fan-out. Generous — even a few thousand tags traverse
// in single-digit milliseconds — but bounded so a stuck DB cannot
// keep a goroutine alive forever after the user moved on.
const notifyTagOrdersTimeout = 5 * time.Second

// maxTagPathDepth bounds the parent-chain walk when building tag
// paths. The schema's FK keeps cycles from happening, but defence in
// depth is cheap and avoids an unbounded loop if anything weird ever
// lands in the table.
const maxTagPathDepth = 64

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
			OrderName:   t.OrderName,
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

// ListPluginOrders queries every running tag_provider plugin live and
// returns the orders grouped by plugin name. The Tag-Manager opens
// this on render so the Auftrag combobox shows current data; the host
// applies a per-plugin timeout so one slow plugin cannot stall the
// tab. A plugin host that is not wired (test path) returns nil.
func (a *App) ListPluginOrders() []pluginhost.Orders {
	if a.pluginHost == nil {
		return nil
	}
	return a.pluginHost.ListAllOrders(a.ctx)
}

// notifyPluginTagOrders builds a snapshot of every tag with its
// current OrderName assignment and pushes it to all running
// tag_provider plugins. Runs in a goroutine so the calling Wails
// method (CreateTag / UpdateTag / DeleteTag) returns immediately —
// the user's UI mutation must never block on plugin RPCs. Both the
// snapshot build and the per-plugin fan-out are best-effort; failures
// are logged but never surfaced to the user.
//
// No-op when the plugin host or the tags repo is not wired (test
// path or PluginsDir empty in the deps).
func (a *App) notifyPluginTagOrders() {
	if a.pluginHost == nil || a.deps.Tags == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), notifyTagOrdersTimeout)
		defer cancel()
		snapshot, err := buildTagOrderSnapshot(ctx, a.deps.Tags)
		if err != nil {
			a.logger.Warn("notify plugin tag orders: snapshot build failed", "err", err)
			return
		}
		a.pluginHost.NotifyTagOrdersChanged(snapshot)
	}()
}

// buildTagOrderSnapshot loads every tag and assembles the
// (path, order_name) snapshot the host pushes to tag_provider plugins.
// Every tag is included regardless of whether OrderName is set — the
// plugin owns the diff against its previous snapshot, so an empty
// OrderName carries information ("currently unmapped") that a filtered
// list would silently drop. The result is sorted by TagPath so the
// plugin's diff does not need a re-sort pass.
//
// Tags whose parent chain cannot be resolved (a dangling FK — should
// not happen given the schema's ON DELETE CASCADE, but guarded) are
// emitted with the partial path the walker could reach. Empty paths
// (e.g. a tag whose normalised name is empty) are dropped.
func buildTagOrderSnapshot(ctx context.Context, repo storage.TagRepository) ([]sdk.TagOrderMapping, error) {
	rows, err := repo.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tags: %w", err)
	}
	byID := make(map[int64]storage.Tag, len(rows))
	for _, t := range rows {
		byID[t.ID] = t
	}
	out := make([]sdk.TagOrderMapping, 0, len(rows))
	for _, t := range rows {
		path := tagPath(byID, t)
		if path == "" {
			continue
		}
		var orderName string
		if t.OrderName != nil {
			orderName = *t.OrderName
		}
		out = append(out, sdk.TagOrderMapping{
			TagPath:   path,
			OrderName: orderName,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TagPath < out[j].TagPath })
	return out, nil
}

// tagPath walks up the parent chain to build a slash-separated path,
// stripping the canonical "#" prefix from each segment so the result
// matches the format tag_provider plugins originally submit via
// sdk.ImportedTag.Path. Bounded at maxTagPathDepth iterations.
func tagPath(byID map[int64]storage.Tag, t storage.Tag) string {
	rev := make([]string, 0, 4)
	cur := t
	for i := 0; i < maxTagPathDepth; i++ {
		rev = append(rev, strings.TrimPrefix(cur.Name, "#"))
		if cur.ParentID == nil {
			break
		}
		next, ok := byID[*cur.ParentID]
		if !ok {
			break
		}
		cur = next
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return strings.Join(rev, "/")
}

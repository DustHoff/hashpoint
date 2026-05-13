package app

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	pluginhost "github.com/dusthoff/hashpoint/internal/plugin"
	"github.com/dusthoff/hashpoint/internal/storage"
	"github.com/dusthoff/hashpoint/internal/tagging"
)

// pluginAutoTagAdapter bridges three layers:
//   - the plugin host (asks every running ProcessAutoTagHandler whether
//     it claims the focused process);
//   - the tag repository (materialises the plugin's slash-separated tag
//     path against the tags table, creating missing nodes);
//   - the tagging orchestrator (consumes the resolved TagID via the
//     tagging.PluginAutoTagResolver interface).
//
// It sits in the app layer because both inputs (host, storage) are
// off-limits to internal/tagging per CLAUDE.md §2.
//
// Materialised tag IDs are cached per (plugin, tag-path) so repeated
// focus events on the same plugin/path skip the DB walk. The cache is
// invalidated lazily: if a cached ID disappears from the DB (a user
// deleted the tag), the next block insertion will fail with an FK
// violation and tagging.Orchestrator will log it — the cache entry
// becomes a no-op once the user toggles the plugin and a fresh
// EnsureByPath repopulates it.
type pluginAutoTagAdapter struct {
	host hostAutoTagResolver
	tags storage.TagRepository
	log  *slog.Logger

	cacheMu sync.RWMutex
	cache   map[string]int64
}

// hostAutoTagResolver is the tiny slice of *pluginhost.Host the adapter
// needs — declared locally so tests can stub it without importing the
// full host. Kept in sync with pluginhost.Host.ResolveProcessAutoTag.
type hostAutoTagResolver interface {
	ResolveProcessAutoTag(ctx context.Context, processName, windowTitle string, isCommunication bool) *pluginhost.ProcessAutoTagResolution
}

// newPluginAutoTagAdapter wires the adapter from its dependencies.
// host or tags == nil ⇒ the adapter is dormant (Resolve always returns
// nil), so callers can wire it unconditionally and the orchestrator
// silently falls through to the no-tag path.
func newPluginAutoTagAdapter(host hostAutoTagResolver, tags storage.TagRepository, logger *slog.Logger) *pluginAutoTagAdapter {
	if logger == nil {
		logger = slog.Default()
	}
	return &pluginAutoTagAdapter{
		host:  host,
		tags:  tags,
		log:   logger,
		cache: map[string]int64{},
	}
}

// Resolve implements tagging.PluginAutoTagResolver. It asks the plugin
// host for a verdict and, on a hit, materialises the plugin-supplied
// tag-name path through storage.TagRepository.EnsureByPath.
func (a *pluginAutoTagAdapter) Resolve(ctx context.Context, processName, windowTitle string, isCommunication bool) *tagging.PluginAutoTagMatch {
	if a == nil || a.host == nil || a.tags == nil {
		return nil
	}
	hit := a.host.ResolveProcessAutoTag(ctx, processName, windowTitle, isCommunication)
	if hit == nil {
		return nil
	}
	tagPath := strings.TrimSpace(hit.TagName)
	if tagPath == "" {
		return nil
	}
	id, ok := a.cacheGet(hit.PluginName, tagPath)
	if !ok {
		tag, err := a.tags.EnsureByPath(ctx, tagPath)
		if err != nil {
			a.log.Warn("plugin auto-tag: ensure tag path failed",
				"plugin", hit.PluginName,
				"path", tagPath,
				"err", err)
			return nil
		}
		id = tag.ID
		a.cacheSet(hit.PluginName, tagPath, id)
	}
	return &tagging.PluginAutoTagMatch{
		PluginName:  hit.PluginName,
		TagID:       id,
		Description: hit.Description,
	}
}

func (a *pluginAutoTagAdapter) cacheGet(pluginName, tagPath string) (int64, bool) {
	a.cacheMu.RLock()
	defer a.cacheMu.RUnlock()
	id, ok := a.cache[cacheKey(pluginName, tagPath)]
	return id, ok
}

func (a *pluginAutoTagAdapter) cacheSet(pluginName, tagPath string, id int64) {
	a.cacheMu.Lock()
	defer a.cacheMu.Unlock()
	a.cache[cacheKey(pluginName, tagPath)] = id
}

// cacheKey concatenates plugin name and tag-path with a NUL separator
// so a plugin named "a|b" cannot collide with plugin "a" + path "b".
func cacheKey(pluginName, tagPath string) string {
	return pluginName + "\x00" + tagPath
}

package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	pluginhost "github.com/onesi/hashpoint/internal/plugin"
	"github.com/onesi/hashpoint/internal/plugin/oncall"
	"github.com/onesi/hashpoint/internal/plugin/sdk"
	"github.com/onesi/hashpoint/internal/storage"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// Wails event names. The frontend subscribes via api.onEventPayload.
const (
	// OnCallSubmitResultEvent fires once per plugin response during a
	// fan-out submission. Payload: OnCallSubmitResultPayload.
	OnCallSubmitResultEvent = "oncall:submit-result"
	// OnCallDocChangedEvent fires whenever a doc row is persisted
	// (Save, Submit completion, MarkStale, Dismiss). Payload: doc id.
	OnCallDocChangedEvent = "oncall:doc-changed"
)

// OnCallSubmitResultPayload is the JSON shape of OnCallSubmitResultEvent.
type OnCallSubmitResultPayload struct {
	DocID        int64  `json:"doc_id"`
	PluginName   string `json:"plugin_name"`
	Status       string `json:"status"` // "submitted" | "failed"
	ExternalRef  string `json:"external_ref,omitempty"`
	ExternalURL  string `json:"external_url,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}

// OnCallDocChangedPayload is the JSON shape of OnCallDocChangedEvent.
type OnCallDocChangedPayload struct {
	DocID int64 `json:"doc_id"`
}

// OnCallDocDraft is the per-block form payload coming from the frontend.
type OnCallDocDraft struct {
	Application  string                     `json:"application"`
	IncidentType storage.OnCallIncidentType `json:"incident_type"`
	Solution     string                     `json:"solution"`
}

// OnCallListFilter is the JSON shape of the inbox filter.
type OnCallListFilter struct {
	Status       string `json:"status,omitempty"`
	From         string `json:"from,omitempty"` // RFC3339
	To           string `json:"to,omitempty"`
	IncludeStale bool   `json:"include_stale,omitempty"`
}

// OnCallDocView is the inbox row + form payload sent to the frontend.
// Joins storage.OnCallDoc with the underlying TagBlock and resolved tag
// name so the frontend never has to re-fetch related rows.
type OnCallDocView struct {
	ID            int64                      `json:"id"`
	BlockID       int64                      `json:"block_id"`
	StartTime     time.Time                  `json:"start_time"`
	EndTime       time.Time                  `json:"end_time"`
	TagID         int64                      `json:"tag_id"`
	TagName       string                     `json:"tag_name"`
	TagAtCreation int64                      `json:"tag_at_creation"`
	Stale         bool                       `json:"stale"`
	Application   string                     `json:"application"`
	IncidentType  storage.OnCallIncidentType `json:"incident_type"`
	Solution      string                     `json:"solution"`
	Status        storage.OnCallDocStatus    `json:"status"`
	Submissions   []OnCallSubmissionView     `json:"submissions,omitempty"`
}

// OnCallSubmissionView is the per-plugin attempt projection.
type OnCallSubmissionView struct {
	PluginName  string `json:"plugin_name"`
	Status      string `json:"status"`
	ExternalRef string `json:"external_ref,omitempty"`
	ExternalURL string `json:"external_url,omitempty"`
	LastError   string `json:"last_error,omitempty"`
	SubmittedAt string `json:"submitted_at,omitempty"` // RFC3339
}

// pluginFieldsAdapter lets the plugin Host read per-plugin field values
// from the App's live Config without a circular import.
type pluginFieldsAdapter struct {
	app *App
}

// PluginFields returns a fresh copy of the field map under [plugins.<name>].
// Concurrent reads with SaveConfig are serialized via App.mu.
func (a pluginFieldsAdapter) PluginFields(name string) map[string]string {
	a.app.mu.Lock()
	defer a.app.mu.Unlock()
	if a.app.cfg == nil || a.app.cfg.Plugins == nil {
		return nil
	}
	src := a.app.cfg.Plugins[name]
	if src == nil {
		return nil
	}
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// onBlockClosedForOnCall is the orchestrator hook the App wires in New().
// It is fire-and-forget from the orchestrator's perspective — already
// dispatched in a goroutine before reaching here — so blocking ops are
// fine. We re-read the latest block state from the repo (the orchestrator
// passes only the ID, deliberately) and run oncall.Recheck.
func (a *App) onBlockClosedForOnCall(ctx context.Context, blockID int64) {
	if a.deps.OnCall == nil {
		return
	}
	block, err := a.deps.TagBlocks.Get(ctx, blockID)
	if err != nil || block == nil {
		// Block may have been deleted between hook fire and read — that
		// is legitimate (e.g. zero-length cleanup); no oncall doc work
		// to do, so silently move on.
		return
	}
	a.recheckOnCall(ctx, *block)
}

// recheckOnCallByID loads the block by ID and runs Recheck. Silently
// skips when the block has been deleted between the mutation and the
// recheck (legitimate race with cascade-delete or zero-length cleanup).
func (a *App) recheckOnCallByID(ctx context.Context, blockID int64) {
	if a.deps.OnCall == nil {
		return
	}
	block, err := a.deps.TagBlocks.Get(ctx, blockID)
	if err != nil || block == nil {
		return
	}
	a.recheckOnCall(ctx, *block)
}

// recheckOnCallOverlapping reconciles every block whose [start,end)
// interval intersects [from, to). Used after a manual-range create where
// the orchestrator may have trimmed/split surrounding blocks.
func (a *App) recheckOnCallOverlapping(ctx context.Context, from, to time.Time) {
	if a.deps.OnCall == nil {
		return
	}
	blocks, err := a.deps.TagBlocks.ListOverlapping(ctx, from, to)
	if err != nil {
		a.logger.Warn("oncall recheck overlap: list failed",
			"from", from, "to", to, "err", err)
		return
	}
	for _, b := range blocks {
		a.recheckOnCall(ctx, b)
	}
}

// recheckOnCall is the single chokepoint App methods use to reconcile a
// block's OnCall doc after a mutation. The caller already verified the
// block exists; this method handles config snapshotting + ancestry
// adapter wiring.
func (a *App) recheckOnCall(ctx context.Context, block storage.TagBlock) {
	if a.deps.OnCall == nil {
		return
	}
	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()
	if cfg == nil {
		return
	}
	ancestry := oncall.TagRepoAncestry{Tags: a.deps.Tags}
	if err := oncall.Recheck(ctx, block, cfg.WorkSchedule, cfg.OnCall.TagIDs, ancestry, a.deps.OnCall); err != nil {
		a.logger.Warn("oncall recheck failed",
			"block_id", block.ID, "err", err)
	}
}

// OnCallDocList returns the inbox rows matching filter, joined with the
// underlying tag block + tag name.
func (a *App) OnCallDocList(filter OnCallListFilter) ([]OnCallDocView, error) {
	if a.deps.OnCall == nil {
		return nil, nil
	}
	f, err := buildOnCallFilter(filter)
	if err != nil {
		return nil, err
	}
	docs, err := a.deps.OnCall.List(a.ctx, f)
	if err != nil {
		return nil, fmt.Errorf("list oncall docs: %w", err)
	}
	views := make([]OnCallDocView, 0, len(docs))
	for _, d := range docs {
		view, err := a.toOnCallDocView(a.ctx, d)
		if err != nil {
			a.logger.Warn("oncall list: skipping unresolvable doc",
				"id", d.ID, "err", err)
			continue
		}
		views = append(views, view)
	}
	return views, nil
}

// OnCallDocGet returns one doc joined with its TagBlock + tag name.
func (a *App) OnCallDocGet(id int64) (*OnCallDocView, error) {
	if a.deps.OnCall == nil {
		return nil, errors.New("oncall feature disabled")
	}
	doc, err := a.deps.OnCall.Get(a.ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get oncall doc: %w", err)
	}
	view, err := a.toOnCallDocView(a.ctx, *doc)
	if err != nil {
		return nil, err
	}
	return &view, nil
}

// OnCallDocSave persists the user's draft fields without dispatching to
// any plugin. Idempotent.
func (a *App) OnCallDocSave(id int64, draft OnCallDocDraft) error {
	if a.deps.OnCall == nil {
		return errors.New("oncall feature disabled")
	}
	if err := a.deps.OnCall.UpdateDraft(a.ctx, id, draft.Application, draft.IncidentType, draft.Solution); err != nil {
		return fmt.Errorf("save oncall draft: %w", err)
	}
	a.emitOnCallDocChanged(id)
	return nil
}

// OnCallDocSubmit kicks off an async fan-out of the doc to every running
// plugin advertising the oncall_documentation capability that does NOT
// already have a successful submission row. Returns immediately; the
// frontend listens on OnCallSubmitResultEvent for per-plugin results.
//
// Returns nil and does NOT change the doc's state when no plugin is
// installed — per product decision, the doc stays in draft.
func (a *App) OnCallDocSubmit(id int64) error {
	if a.deps.OnCall == nil {
		return errors.New("oncall feature disabled")
	}
	doc, err := a.deps.OnCall.Get(a.ctx, id)
	if err != nil {
		return fmt.Errorf("get oncall doc: %w", err)
	}
	view, err := a.toOnCallDocView(a.ctx, *doc)
	if err != nil {
		return fmt.Errorf("build view: %w", err)
	}
	if a.pluginHost == nil {
		// Doc stays in draft. The frontend can show a "no plugin
		// installed" hint based on plugin list being empty.
		a.logger.Info("oncall submit skipped — plugin host not configured",
			"doc_id", id)
		return nil
	}

	// Build the SDK payload once.
	payload := sdk.OnCallDocument{
		LocalID:      fmt.Sprintf("hashpoint:oncall:%d", doc.ID),
		BlockID:      doc.BlockID,
		StartTime:    view.StartTime.UTC(),
		EndTime:      view.EndTime.UTC(),
		TagName:      view.TagName,
		Application:  doc.Application,
		IncidentType: sdk.IncidentType(doc.IncidentType),
		Solution:     doc.Solution,
	}

	// Compute the set of plugins we want to dispatch to: every running
	// oncall plugin that does NOT already have status=submitted on this
	// doc. Existing failed/pending rows are reset to pending here so the
	// inbox immediately shows the spinner.
	infos := a.pluginHost.List()
	skip := submittedPlugins(doc.Submissions)

	dispatched := false
	for _, p := range infos {
		if p.State != pluginhost.StateRunning {
			continue
		}
		if !hasCapability(p.Capabilities, sdk.CapOnCallDocumentation) {
			continue
		}
		if skip[p.Name] {
			continue
		}
		sub, err := a.deps.OnCall.EnsureSubmission(a.ctx, doc.ID, p.Name)
		if err != nil {
			a.logger.Warn("oncall submit: cannot ensure submission row",
				"doc_id", doc.ID, "plugin", p.Name, "err", err)
			continue
		}
		if sub.Status == "failed" {
			if err := a.deps.OnCall.MarkSubmissionPending(a.ctx, sub.ID); err != nil {
				a.logger.Warn("oncall submit: cannot reset to pending",
					"sub_id", sub.ID, "err", err)
				continue
			}
		}
		dispatched = true
	}
	if !dispatched {
		// No eligible plugins. Keep the doc as-is.
		a.emitOnCallDocChanged(doc.ID)
		return nil
	}
	a.emitOnCallDocChanged(doc.ID)

	// Fire-and-forget the actual fan-out. The host's per-plugin timeout
	// caps each call; the goroutine joins inside SubmitOnCallDoc and
	// then emits the final "doc-changed" so the inbox refreshes.
	go a.runOnCallSubmit(doc.ID, payload, skip)
	return nil
}

// OnCallDocDismiss deletes a doc that the user no longer wants to keep.
// Refuses if any submission has a non-pending state — we never silently
// drop a doc whose remote ticket already exists.
func (a *App) OnCallDocDismiss(id int64) error {
	if a.deps.OnCall == nil {
		return errors.New("oncall feature disabled")
	}
	doc, err := a.deps.OnCall.Get(a.ctx, id)
	if err != nil {
		return fmt.Errorf("get oncall doc: %w", err)
	}
	for _, s := range doc.Submissions {
		if s.Status != "pending" {
			return fmt.Errorf("dismiss refused: submission to %q is %s; delete the remote ticket manually first", s.PluginName, s.Status)
		}
	}
	if err := a.deps.OnCall.Dismiss(a.ctx, id); err != nil {
		return fmt.Errorf("dismiss oncall doc: %w", err)
	}
	a.emitOnCallDocChanged(id)
	return nil
}

// PluginList returns the read-model the settings UI consumes.
func (a *App) PluginList() ([]pluginhost.PluginInfo, error) {
	if a.pluginHost == nil {
		return nil, nil
	}
	return a.pluginHost.List(), nil
}

// PluginGetConfig returns the persisted [plugins.<name>] section.
func (a *App) PluginGetConfig(name string) (map[string]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cfg == nil || a.cfg.Plugins == nil {
		return map[string]string{}, nil
	}
	src := a.cfg.Plugins[name]
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out, nil
}

// PluginSetConfig replaces the [plugins.<name>] section and reloads the
// plugin so the new fields take effect. Secrets are NOT included here —
// PluginSetSecret writes to the credential store independently.
func (a *App) PluginSetConfig(name string, fields map[string]string) error {
	a.mu.Lock()
	if a.cfg == nil {
		a.mu.Unlock()
		return errors.New("config not loaded")
	}
	if a.cfg.Plugins == nil {
		a.cfg.Plugins = map[string]map[string]string{}
	}
	clone := make(map[string]string, len(fields))
	for k, v := range fields {
		clone[k] = v
	}
	a.cfg.Plugins[name] = clone
	cfgCopy := *a.cfg
	a.mu.Unlock()
	if a.deps.OnConfigSet != nil {
		if err := a.deps.OnConfigSet(&cfgCopy); err != nil {
			return fmt.Errorf("persist config: %w", err)
		}
	}
	if a.pluginHost == nil {
		return nil
	}
	return a.pluginHost.Reload(a.ctx, name)
}

// PluginSetSecret writes a per-plugin secret to the credential store. The
// plugin is reloaded so the new secret takes effect — handles minted
// before the change become stale and ErrUnknownSecretHandle replays will
// surface if the plugin still holds them.
func (a *App) PluginSetSecret(name, key, value string) error {
	if a.pluginHost == nil {
		return errors.New("plugin host not configured")
	}
	if err := a.pluginHost.SetSecret(name, key, value); err != nil {
		return err
	}
	return a.pluginHost.Reload(a.ctx, name)
}

// PluginDeleteSecret removes a per-plugin secret + reloads.
func (a *App) PluginDeleteSecret(name, key string) error {
	if a.pluginHost == nil {
		return errors.New("plugin host not configured")
	}
	if err := a.pluginHost.DeleteSecret(name, key); err != nil {
		return err
	}
	return a.pluginHost.Reload(a.ctx, name)
}

// PluginReload rebuilds the named plugin (e.g. after a manual binary swap).
func (a *App) PluginReload(name string) error {
	if a.pluginHost == nil {
		return errors.New("plugin host not configured")
	}
	return a.pluginHost.Reload(a.ctx, name)
}

// --- helpers ------------------------------------------------------------

// runOnCallSubmit performs the actual fan-out in a goroutine. Per-plugin
// results are persisted + emitted via OnCallSubmitResultEvent; once the
// fan-out joins, a final OnCallDocChangedEvent triggers an inbox refresh.
func (a *App) runOnCallSubmit(docID int64, payload sdk.OnCallDocument, skipPlugins map[string]bool) {
	if a.pluginHost == nil {
		return
	}
	var mu sync.Mutex // serialises Sink callbacks since hplugin invokes them concurrently
	sink := func(r pluginhost.SubmitResult) {
		mu.Lock()
		defer mu.Unlock()
		if skipPlugins[r.PluginName] {
			return
		}
		a.persistSubmissionResult(docID, r)
	}
	if err := a.pluginHost.SubmitOnCallDoc(a.ctx, payload, sink); err != nil && !errors.Is(err, pluginhost.ErrNoOnCallPlugin) {
		a.logger.Warn("oncall submit fan-out failed", "doc_id", docID, "err", err)
	}
	a.emitOnCallDocChanged(docID)
}

// persistSubmissionResult writes one plugin's result and fires the per-
// plugin frontend event. Errors are logged but never propagated — the
// caller is a fire-and-forget goroutine.
func (a *App) persistSubmissionResult(docID int64, r pluginhost.SubmitResult) {
	now := time.Now().UTC()
	sub, err := a.deps.OnCall.EnsureSubmission(a.ctx, docID, r.PluginName)
	if err != nil {
		a.logger.Warn("oncall: cannot ensure submission row on result",
			"doc_id", docID, "plugin", r.PluginName, "err", err)
		return
	}
	if r.Err != nil {
		if err := a.deps.OnCall.MarkSubmissionFailed(a.ctx, sub.ID, r.Err.Error(), now); err != nil {
			a.logger.Warn("oncall: cannot persist failed result",
				"sub_id", sub.ID, "err", err)
		}
		a.emitOnCallSubmitResult(OnCallSubmitResultPayload{
			DocID:        docID,
			PluginName:   r.PluginName,
			Status:       "failed",
			ErrorMessage: r.Err.Error(),
		})
		return
	}
	if err := a.deps.OnCall.MarkSubmissionSubmitted(a.ctx, sub.ID, r.Result.ExternalRef, r.Result.ExternalURL, now); err != nil {
		a.logger.Warn("oncall: cannot persist successful result",
			"sub_id", sub.ID, "err", err)
	}
	a.emitOnCallSubmitResult(OnCallSubmitResultPayload{
		DocID:       docID,
		PluginName:  r.PluginName,
		Status:      "submitted",
		ExternalRef: r.Result.ExternalRef,
		ExternalURL: r.Result.ExternalURL,
	})
}

// toOnCallDocView joins a doc with its block + tag name.
func (a *App) toOnCallDocView(ctx context.Context, d storage.OnCallDoc) (OnCallDocView, error) {
	block, err := a.deps.TagBlocks.Get(ctx, d.BlockID)
	if err != nil {
		return OnCallDocView{}, fmt.Errorf("get block %d: %w", d.BlockID, err)
	}
	var endTime time.Time
	if block.EndTime != nil {
		endTime = *block.EndTime
	}
	tagName := ""
	if tag, err := a.deps.Tags.Get(ctx, block.TagID); err == nil && tag != nil {
		tagName = tag.Name
	}
	view := OnCallDocView{
		ID:            d.ID,
		BlockID:       d.BlockID,
		StartTime:     block.StartTime,
		EndTime:       endTime,
		TagID:         block.TagID,
		TagName:       tagName,
		TagAtCreation: d.TagAtCreation,
		Stale:         d.Stale,
		Application:   d.Application,
		IncidentType:  d.IncidentType,
		Solution:      d.Solution,
		Status:        d.Status(),
		Submissions:   submissionsToView(d.Submissions),
	}
	return view, nil
}

func submissionsToView(subs []storage.OnCallSubmission) []OnCallSubmissionView {
	if len(subs) == 0 {
		return nil
	}
	out := make([]OnCallSubmissionView, 0, len(subs))
	for _, s := range subs {
		v := OnCallSubmissionView{
			PluginName: s.PluginName,
			Status:     s.Status,
		}
		if s.ExternalRef != nil {
			v.ExternalRef = *s.ExternalRef
		}
		if s.ExternalURL != nil {
			v.ExternalURL = *s.ExternalURL
		}
		if s.LastError != nil {
			v.LastError = *s.LastError
		}
		if s.SubmittedAt != nil {
			v.SubmittedAt = s.SubmittedAt.UTC().Format(time.RFC3339)
		}
		out = append(out, v)
	}
	return out
}

func buildOnCallFilter(in OnCallListFilter) (storage.OnCallFilter, error) {
	out := storage.OnCallFilter{IncludeStale: in.IncludeStale}
	if in.Status != "" {
		s := storage.OnCallDocStatus(in.Status)
		out.Status = &s
	}
	if in.From != "" {
		t, err := time.Parse(time.RFC3339, in.From)
		if err != nil {
			return out, fmt.Errorf("parse from: %w", err)
		}
		tt := t.UTC()
		out.From = &tt
	}
	if in.To != "" {
		t, err := time.Parse(time.RFC3339, in.To)
		if err != nil {
			return out, fmt.Errorf("parse to: %w", err)
		}
		tt := t.UTC()
		out.To = &tt
	}
	return out, nil
}

func submittedPlugins(subs []storage.OnCallSubmission) map[string]bool {
	out := map[string]bool{}
	for _, s := range subs {
		if s.Status == "submitted" {
			out[s.PluginName] = true
		}
	}
	return out
}

func hasCapability(caps []sdk.Capability, want sdk.Capability) bool {
	for _, c := range caps {
		if c == want {
			return true
		}
	}
	return false
}

func (a *App) emitOnCallDocChanged(docID int64) {
	if a.ctx == nil {
		return
	}
	wailsruntime.EventsEmit(a.ctx, OnCallDocChangedEvent, OnCallDocChangedPayload{DocID: docID})
}

func (a *App) emitOnCallSubmitResult(p OnCallSubmitResultPayload) {
	if a.ctx == nil {
		return
	}
	wailsruntime.EventsEmit(a.ctx, OnCallSubmitResultEvent, p)
}

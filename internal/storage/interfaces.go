package storage

import (
	"context"
	"time"
)

// ProcessTrackRepository persists raw window-focus events. Process tracks
// have no overlap constraint at the storage layer — the tracker is the
// single writer and emits events in chronological order, so blocks are
// disjoint by construction.
type ProcessTrackRepository interface {
	// Open starts a new track; the returned ID is set on the track.
	Open(ctx context.Context, p *ProcessTrack) error
	// Close finalizes the track by setting its end time and computed duration.
	Close(ctx context.Context, id int64, end time.Time) error
	// MarkIdle finalizes the track as idle.
	MarkIdle(ctx context.Context, id int64, end time.Time) error
	// LastOpen returns the most recently started open track, or nil.
	LastOpen(ctx context.Context) (*ProcessTrack, error)
	// ListOpen returns every focused track whose end_time is NULL, ascending.
	// Communication tracks are excluded; use ListOpenCommunication.
	ListOpen(ctx context.Context) ([]ProcessTrack, error)
	// ListOpenCommunication returns every open communication track, ascending.
	// Used by tracker recovery to close dangling comm tracks at startup.
	ListOpenCommunication(ctx context.Context) ([]ProcessTrack, error)
	// ListByDay returns all tracks whose start_time falls on the given UTC day.
	ListByDay(ctx context.Context, day time.Time) ([]ProcessTrack, error)
	// ListBetween returns all tracks in [from, to).
	ListBetween(ctx context.Context, from, to time.Time) ([]ProcessTrack, error)
	// LastEnd returns the end_time of the most recently closed track, or zero
	// time when no closed track exists. Used by the startup-cleanup hook to
	// compute a sensible end for any dangling open-ended manual tag.
	LastEnd(ctx context.Context) (time.Time, error)
	// Get fetches a single track by ID.
	Get(ctx context.Context, id int64) (*ProcessTrack, error)
}

// TagBlockRepository persists tagged time spans. The repo enforces the
// non-overlap invariant: tagging spans synced to Personio must not collide,
// and the storage layer is the last line of defense.
type TagBlockRepository interface {
	// Open inserts a new tag block. Refuses the write if it would overlap
	// any other tag block.
	Open(ctx context.Context, b *TagBlock) error
	// Close finalizes an open tag block by setting its end time.
	Close(ctx context.Context, id int64, end time.Time) error
	// SetEnd is Close without the "must be still open" precondition — used
	// when shrinking an existing closed block (e.g. manual range tag carving
	// space out of an auto block).
	SetEnd(ctx context.Context, id int64, end time.Time) error
	// SetStart shrinks a tag block by moving its start_time forward.
	SetStart(ctx context.Context, id int64, start time.Time) error
	// Resize updates both start_time and end_time of a closed tag block in
	// one transaction. Refuses if the block is open or if the new range
	// would overlap any other tag block. When promoteToManual is true and
	// the block is currently auto, the row is also flipped to is_manual=1
	// (the user's resize is a manual intervention).
	Resize(ctx context.Context, id int64, start, end time.Time, promoteToManual bool) error
	// SetTag re-points a tag block to a different tag (used during
	// re-tagging a manual range).
	SetTag(ctx context.Context, id, tagID int64) error
	// SetDescription writes the activity description (nil clears it).
	SetDescription(ctx context.Context, id int64, description *string) error
	// MarkSynced records the Personio attendance ID.
	MarkSynced(ctx context.Context, id int64, personioID string, at time.Time) error
	// LastOpen returns the most recently started open tag block, or nil.
	LastOpen(ctx context.Context) (*TagBlock, error)
	// ListOpen returns every tag block whose end_time is NULL, ascending.
	ListOpen(ctx context.Context) ([]TagBlock, error)
	// ListOpenManual returns every open-ended manual tag block, ascending.
	// At most one should ever be present; callers treat anything else as a
	// startup-cleanup target.
	ListOpenManual(ctx context.Context) ([]TagBlock, error)
	// ListByDay returns all tag blocks whose start_time falls on the given
	// UTC day.
	ListByDay(ctx context.Context, day time.Time) ([]TagBlock, error)
	// ListBetween returns all tag blocks in [from, to).
	ListBetween(ctx context.Context, from, to time.Time) ([]TagBlock, error)
	// ListOverlapping returns every tag block whose interval intersects
	// [from, to). Used by the manual-range workflow to find auto blocks
	// that need trimming or splitting.
	ListOverlapping(ctx context.Context, from, to time.Time) ([]TagBlock, error)
	// Get fetches a single tag block by ID.
	Get(ctx context.Context, id int64) (*TagBlock, error)
	// Delete removes a tag block.
	Delete(ctx context.Context, id int64) error
	// RecentlyUsedTagIDs returns up to `limit` tag IDs ordered by their most
	// recent block start time, restricted to blocks started at or after
	// `since`. Used by the quick-tag-picker.
	RecentlyUsedTagIDs(ctx context.Context, since time.Time, limit int) ([]int64, error)
	// LatestUnsyncedDayBefore returns the local-day midnight (in loc) of the
	// most recent calendar day before `cutoff` that contains at least one
	// closed tag block with synced_at IS NULL. Returns ok=false when no such
	// day exists. Used by the startup-sync to pick the day to push to
	// Personio without re-syncing days that are already complete.
	LatestUnsyncedDayBefore(ctx context.Context, cutoff time.Time, loc *time.Location) (time.Time, bool, error)
}

// TagRepository persists tag hierarchies.
type TagRepository interface {
	Create(ctx context.Context, t *Tag) error
	Update(ctx context.Context, t *Tag) error
	Delete(ctx context.Context, id int64) error
	Get(ctx context.Context, id int64) (*Tag, error)
	List(ctx context.Context) ([]Tag, error)
	Children(ctx context.Context, parentID int64) ([]Tag, error)
}

// RuleRepository persists auto-tagging rules.
type RuleRepository interface {
	Create(ctx context.Context, r *Rule) error
	Update(ctx context.Context, r *Rule) error
	Delete(ctx context.Context, id int64) error
	Get(ctx context.Context, id int64) (*Rule, error)
	// ListEnabled returns enabled rules sorted by priority DESC, id ASC.
	ListEnabled(ctx context.Context) ([]Rule, error)
	List(ctx context.Context) ([]Rule, error)
}

// SettingsRepository is a simple key/value store for runtime settings (e.g.
// last sync timestamp, OAuth refresh token cache).
type SettingsRepository interface {
	Get(ctx context.Context, key string) (string, bool, error)
	Set(ctx context.Context, key, value string) error
	Delete(ctx context.Context, key string) error
}

// PluginSettingsRepository persists per-plugin configuration:
//
//   - plugin_state holds the user-controlled enable flag. A plugin that
//     has never been touched (no row) defaults to enabled, so newly
//     discovered plugins run on first start without an opt-in step.
//   - plugin_settings holds the user-entered config values. Rows with
//     is_secret=1 are stored encrypted (DPAPI in production) and are
//     decrypted on read so callers always work with cleartext.
//
// The split between state and settings tables is deliberate: a
// disable→enable cycle preserves previously-entered field values
// because they sit in a different table than the toggle.
type PluginSettingsRepository interface {
	// GetEnabled returns whether the plugin is currently enabled. A
	// plugin with no row defaults to true.
	GetEnabled(ctx context.Context, name string) (bool, error)
	// SetEnabled upserts the state row.
	SetEnabled(ctx context.Context, name string, enabled bool) error
	// GetFields returns the plain and secret values for the plugin's
	// settings. Both maps are non-nil even when empty; secret values
	// are decrypted in-place.
	GetFields(ctx context.Context, name string) (plain map[string]string, secrets map[string]string, err error)
	// GetSecret returns the decrypted value for a single secret key,
	// with found=false when the row is absent. Cheaper than GetFields
	// when callers only need one value.
	GetSecret(ctx context.Context, name, key string) (value string, found bool, err error)
	// SetField upserts a plain field.
	SetField(ctx context.Context, name, key, value string) error
	// SetSecretField upserts a value that is encrypted at rest.
	SetSecretField(ctx context.Context, name, key, value string) error
	// DeleteField removes one row (plain or secret). Missing rows are
	// not an error.
	DeleteField(ctx context.Context, name, key string) error
	// Clear removes every row for the plugin (settings + state). Used
	// when a plugin is uninstalled.
	Clear(ctx context.Context, name string) error
}

// OnCallFilter restricts OnCallRepository.List to a slice of the inbox.
// A nil pointer means "no filter on that dimension". An empty IncludeStale
// false means stale rows are excluded (the inbox default); set to true to
// surface them with the "tag changed" banner.
type OnCallFilter struct {
	Status       *OnCallDocStatus
	From         *time.Time // UTC, inclusive — filters tag block start_time
	To           *time.Time // UTC, exclusive
	IncludeStale bool
}

// OnCallRepository persists on-call ("Rufbereitschaft") documentation rows
// and their per-plugin submission attempts. Two tables back this interface:
//
//   - oncall_documentations: one row per tag block (UNIQUE block_id),
//     created lazily by EnsureForBlock when the orchestrator decides the
//     block qualifies for off-duty documentation.
//
//   - oncall_submissions: one row per (doc, plugin), inserted on the first
//     fan-out and updated as the plugin reports back. Successful rows are
//     final; failed rows are re-dispatched on retry.
//
// All reads load submissions via a follow-up query and zip them onto the
// returned OnCallDoc.Submissions slice — the rolled-up status is computed
// in Go (OnCallDoc.Status()) rather than stored.
type OnCallRepository interface {
	// EnsureForBlock is idempotent: returns the existing doc if one is
	// already linked to blockID, or inserts a fresh one otherwise.
	// tagAtCreation captures the block's current TagID so the orchestrator
	// can detect drift later (re-tag, resize out of off-hours → mark stale).
	EnsureForBlock(ctx context.Context, blockID, tagAtCreation int64) (*OnCallDoc, error)
	// GetByBlock returns the doc + its submissions, or ErrNotFound.
	GetByBlock(ctx context.Context, blockID int64) (*OnCallDoc, error)
	// Get returns the doc + its submissions by primary key, or ErrNotFound.
	Get(ctx context.Context, id int64) (*OnCallDoc, error)
	// List returns docs matching filter, joined with their submissions,
	// ordered by the block's start_time descending (newest first — the
	// inbox shows the latest off-duty incident at the top).
	List(ctx context.Context, filter OnCallFilter) ([]OnCallDoc, error)
	// UpdateDraft writes the user's form input. Safe to call repeatedly;
	// updated_at is touched on every call.
	UpdateDraft(ctx context.Context, id int64, application string, incidentType OnCallIncidentType, solution string) error
	// MarkStale sets stale=1. Idempotent.
	MarkStale(ctx context.Context, id int64) error
	// ClearStale sets stale=0. Idempotent.
	ClearStale(ctx context.Context, id int64) error
	// Dismiss deletes the doc (and cascades its submissions). Used when the
	// user clicks "Discard" on a stale row; the host refuses to dismiss a
	// doc that has any non-pending submission, to avoid losing references
	// to remote tickets that were actually filed.
	Dismiss(ctx context.Context, id int64) error
	// DeleteByBlock removes the doc tied to blockID, if any. FK cascade
	// covers the tag-block delete path, but this lets the orchestrator
	// reconcile explicitly when needed.
	DeleteByBlock(ctx context.Context, blockID int64) error

	// EnsureSubmission inserts (or returns) the submission row for
	// (docID, pluginName). The fresh row's status is 'pending'; an
	// existing row's status is left as-is so retries can transition
	// only failed rows back to pending via MarkSubmissionPending.
	EnsureSubmission(ctx context.Context, docID int64, pluginName string) (*OnCallSubmission, error)
	// MarkSubmissionPending resets a failed/submitted row to pending —
	// used at the start of a retry dispatch. Callers should restrict
	// this to rows in state 'failed' (idempotent for that case).
	MarkSubmissionPending(ctx context.Context, id int64) error
	// MarkSubmissionSubmitted records a successful plugin response.
	MarkSubmissionSubmitted(ctx context.Context, id int64, externalRef, externalURL string, at time.Time) error
	// MarkSubmissionFailed records a plugin error.
	MarkSubmissionFailed(ctx context.Context, id int64, errMsg string, at time.Time) error
	// ListSubmissionsByDoc returns the per-plugin attempts for docID.
	ListSubmissionsByDoc(ctx context.Context, docID int64) ([]OnCallSubmission, error)
}

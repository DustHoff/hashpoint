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
	// ListOpen returns every track whose end_time is NULL, ascending.
	ListOpen(ctx context.Context) ([]ProcessTrack, error)
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

package storage

import (
	"context"
	"time"
)

// FocusBlockRepository persists tracked focus intervals.
type FocusBlockRepository interface {
	// Open starts a new block; the returned ID is set on the block.
	Open(ctx context.Context, b *FocusBlock) error
	// Close finalizes the block by setting its end time and computed duration.
	Close(ctx context.Context, id int64, end time.Time) error
	// MarkIdle finalizes the block as idle.
	MarkIdle(ctx context.Context, id int64, end time.Time) error
	// LastOpen returns the currently open block (if any) — used for crash recovery.
	LastOpen(ctx context.Context) (*FocusBlock, error)
	// ListByDay returns all blocks whose start_time falls on the given UTC day.
	ListByDay(ctx context.Context, day time.Time) ([]FocusBlock, error)
	// ListBetween returns all blocks in [from, to).
	ListBetween(ctx context.Context, from, to time.Time) ([]FocusBlock, error)
	// SetTag assigns or clears the tag of a block. autoTagged flags whether the
	// assignment came from the rules engine.
	SetTag(ctx context.Context, id int64, tagID *int64, autoTagged bool) error
	// MarkSynced records the Personio attendance ID for the block.
	MarkSynced(ctx context.Context, id int64, personioID string, at time.Time) error
	// Split splits a block at the given UTC time. Returns the new (right) block.
	Split(ctx context.Context, id int64, at time.Time) (*FocusBlock, error)
	// Update writes editable fields (start_time, end_time, window_title) back.
	Update(ctx context.Context, b *FocusBlock) error
	// Delete removes a block.
	Delete(ctx context.Context, id int64) error
	// Get fetches a single block by ID.
	Get(ctx context.Context, id int64) (*FocusBlock, error)
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

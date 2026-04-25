// Package storage provides the SQLite-backed persistence layer.
//
// All times are stored in UTC. The package exposes typed repositories
// (FocusBlockRepo, TagRepo, RuleRepo, SettingsRepo) behind interfaces declared
// in interfaces.go for easy testing and clear ownership.
package storage

import "time"

// FocusBlock is a single tracked focus interval.
type FocusBlock struct {
	ID          int64      `json:"id"`
	ProcessName string     `json:"process_name"`
	ProcessPath string     `json:"process_path,omitempty"`
	WindowTitle string     `json:"window_title"`
	StartTime   time.Time  `json:"start_time"`
	EndTime     *time.Time `json:"end_time,omitempty"`
	DurationSec int64      `json:"duration_sec"`
	IsIdle      bool       `json:"is_idle"`
	TagID       *int64     `json:"tag_id,omitempty"`
	AutoTagged  bool       `json:"auto_tagged"`
	Description *string    `json:"description,omitempty"`
	PersonioID  *string    `json:"personio_id,omitempty"`
	SyncedAt    *time.Time `json:"synced_at,omitempty"`
	// IsPlaceholder marks synthetic blocks the user created via the timeline
	// drag-range workflow to extend a tagged period past actually tracked
	// activity. They have no real process metadata and are deleted again when
	// their tag is cleared.
	IsPlaceholder bool `json:"is_placeholder"`
}

// IsOpen returns true while the block is still being recorded (no end time).
func (b FocusBlock) IsOpen() bool { return b.EndTime == nil }

// Tag represents a hierarchical tag (Parent or Sub).
type Tag struct {
	ID                 int64     `json:"id"`
	ParentID           *int64    `json:"parent_id,omitempty"`
	Name               string    `json:"name"`
	Description        *string   `json:"description,omitempty"`
	Color              *string   `json:"color,omitempty"`
	PersonioProjectID  *string   `json:"personio_project_id,omitempty"`
	PersonioActivityID *string   `json:"personio_activity_id,omitempty"`
	SyncToPersonio     bool      `json:"sync_to_personio"`
	CreatedAt          time.Time `json:"created_at"`
}

// IsSubTag reports whether the tag has a parent.
func (t Tag) IsSubTag() bool { return t.ParentID != nil }

// MatchField identifies which fields of a focus block a rule matches against.
type MatchField string

// MatchType identifies how the pattern is interpreted.
type MatchType string

const (
	// MatchProcessName matches against the process name only.
	MatchProcessName MatchField = "process_name"
	// MatchWindowTitle matches against the window title only.
	MatchWindowTitle MatchField = "window_title"
	// MatchBoth matches if both process and title match.
	MatchBoth MatchField = "both"

	// MatchContains performs a case-insensitive substring match.
	MatchContains MatchType = "contains"
	// MatchEquals performs a case-insensitive equality check.
	MatchEquals MatchType = "equals"
	// MatchRegex compiles the pattern as RE2 regex.
	MatchRegex MatchType = "regex"
)

// Rule is an auto-tagging rule.
type Rule struct {
	ID         int64      `json:"id"`
	MatchField MatchField `json:"match_field"`
	MatchType  MatchType  `json:"match_type"`
	Pattern    string     `json:"pattern"`
	TagID      int64      `json:"tag_id"`
	Priority   int        `json:"priority"`
	Enabled    bool       `json:"enabled"`
	CreatedAt  time.Time  `json:"created_at"`
}

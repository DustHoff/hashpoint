// Package storage provides the SQLite-backed persistence layer.
//
// All times are stored in UTC. The package exposes typed repositories
// (ProcessTrackRepo, TagBlockRepo, TagRepo, RuleRepo, SettingsRepo) behind
// interfaces declared in interfaces.go for easy testing and clear ownership.
//
// Process tracks and tag blocks are deliberately separate concerns:
//   - ProcessTrack represents a raw window-focus event from the polling loop.
//     It carries process / window metadata and reflects exactly what was on
//     screen, with no granularity snapping or tagging state.
//   - TagBlock represents a tagged span (manual or auto). It snaps to the
//     configured granularity, lives independently of any process event, and
//     is the unit synced to Personio.
package storage

import "time"

// ProcessTrack is a single window-focus interval recorded by the tracker.
// Process tracks are immutable once closed and are never split or trimmed
// by tagging operations — tag spans overlay them rather than mutating them.
type ProcessTrack struct {
	ID          int64      `json:"id"`
	ProcessName string     `json:"process_name"`
	ProcessPath string     `json:"process_path,omitempty"`
	WindowTitle string     `json:"window_title"`
	StartTime   time.Time  `json:"start_time"`
	EndTime     *time.Time `json:"end_time,omitempty"`
	DurationSec int64      `json:"duration_sec"`
	IsIdle      bool       `json:"is_idle"`
}

// IsOpen returns true while the track is still being recorded.
func (p ProcessTrack) IsOpen() bool { return p.EndTime == nil }

// TagBlock is a tagging span. Manual blocks come from explicit user
// assignment (tray submenu, drag-and-tag); auto blocks are emitted by the
// tagging orchestrator when a process matches a rule.
//
// At most one open-ended manual tag block exists at any time — the
// orchestrator enforces that invariant. End times are floored to the
// configured granularity; zero-length spans are never persisted.
type TagBlock struct {
	ID          int64      `json:"id"`
	TagID       int64      `json:"tag_id"`
	Description *string    `json:"description,omitempty"`
	StartTime   time.Time  `json:"start_time"`
	EndTime     *time.Time `json:"end_time,omitempty"`
	DurationSec int64      `json:"duration_sec"`
	IsManual    bool       `json:"is_manual"`
	PersonioID  *string    `json:"personio_id,omitempty"`
	SyncedAt    *time.Time `json:"synced_at,omitempty"`
}

// IsOpen returns true while the tag block is still active (no end time).
func (b TagBlock) IsOpen() bool { return b.EndTime == nil }

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

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
//
// Two flavours coexist in this table:
//   - Focused tracks (IsCommunication=false): one open at a time, disjoint
//     by construction — the tracker closes the previous before opening the
//     next on focus change.
//   - Communication tracks (IsCommunication=true): opened in parallel for
//     every visible top-level window owned by a process listed in
//     CommunicationConfig.ProcessNames. They overlap freely with focused
//     tracks and with each other; the timeline renders them on a separate
//     rail.
type ProcessTrack struct {
	ID              int64      `json:"id"`
	ProcessName     string     `json:"process_name"`
	ProcessPath     string     `json:"process_path,omitempty"`
	WindowTitle     string     `json:"window_title"`
	StartTime       time.Time  `json:"start_time"`
	EndTime         *time.Time `json:"end_time,omitempty"`
	DurationSec     int64      `json:"duration_sec"`
	IsIdle          bool       `json:"is_idle"`
	IsCommunication bool       `json:"is_communication"`
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
	// OrderName is the user's external-order mapping for this tag —
	// either a name supplied by a tag_provider plugin's order
	// catalogue or arbitrary freitext. Optional; nil ⇒ no mapping.
	// Phase 1 stores and displays the value; it does not feed into
	// Personio sync or auto-tagging.
	OrderName *string   `json:"order_name,omitempty"`
	CreatedAt time.Time `json:"created_at"`
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
//
// Description, when non-empty, is copied verbatim onto the
// tag_blocks.description of every auto-tag block opened by this rule.
// It is appended to the Personio comment via the standard comment
// pipeline (see §2.5.3).
type Rule struct {
	ID          int64      `json:"id"`
	MatchField  MatchField `json:"match_field"`
	MatchType   MatchType  `json:"match_type"`
	Pattern     string     `json:"pattern"`
	TagID       int64      `json:"tag_id"`
	Description *string    `json:"description,omitempty"`
	Priority    int        `json:"priority"`
	Enabled     bool       `json:"enabled"`
	CreatedAt   time.Time  `json:"created_at"`
}

// OnCallDocStatus is the roll-up status of an on-call documentation row,
// computed from the per-plugin Submissions slice. It is never stored.
type OnCallDocStatus string

const (
	// OnCallStatusDraft means the doc exists but has never been submitted —
	// no Submissions rows. Edits stay local.
	OnCallStatusDraft OnCallDocStatus = "draft"
	// OnCallStatusPending means at least one submission is still in flight.
	OnCallStatusPending OnCallDocStatus = "pending"
	// OnCallStatusSubmitted means every submission returned ok.
	OnCallStatusSubmitted OnCallDocStatus = "submitted"
	// OnCallStatusPartial means a mix of submitted and failed (no pending).
	// Retry will only re-dispatch the failed ones.
	OnCallStatusPartial OnCallDocStatus = "partial"
	// OnCallStatusFailed means every submission failed (no pending).
	OnCallStatusFailed OnCallDocStatus = "failed"
)

// OnCallIncidentType discriminates the two flavours the off-duty form
// supports. The empty string is a legal in-progress value before the user
// has made a selection.
type OnCallIncidentType string

const (
	// OnCallIncidentPlannedMaintenance covers expected work performed
	// during the on-call window (patching, scheduled migrations, …).
	OnCallIncidentPlannedMaintenance OnCallIncidentType = "planned_maintenance"
	// OnCallIncidentServiceDisruption covers unexpected outages the
	// on-caller responded to.
	OnCallIncidentServiceDisruption OnCallIncidentType = "service_disruption"
)

// OnCallDoc is the documentation captured for a single off-duty tag block.
// At most one doc per block (UNIQUE index on block_id). The Submissions
// slice is loaded on read (List/Get) and is not stored on the doc row
// itself — see OnCallSubmission.
type OnCallDoc struct {
	ID            int64              `json:"id"`
	BlockID       int64              `json:"block_id"`
	TagAtCreation int64              `json:"tag_at_creation"`
	Stale         bool               `json:"stale"`
	Application   string             `json:"application"`
	IncidentType  OnCallIncidentType `json:"incident_type"`
	Solution      string             `json:"solution"`
	CreatedAt     time.Time          `json:"created_at"`
	UpdatedAt     time.Time          `json:"updated_at"`
	Submissions   []OnCallSubmission `json:"submissions,omitempty"`
}

// Status rolls up the per-plugin Submissions into a single status the UI
// can render. Pure function over the slice — never queries the DB.
func (d OnCallDoc) Status() OnCallDocStatus {
	if len(d.Submissions) == 0 {
		return OnCallStatusDraft
	}
	var pending, submitted, failed int
	for _, s := range d.Submissions {
		switch s.Status {
		case "pending":
			pending++
		case "submitted":
			submitted++
		case "failed":
			failed++
		}
	}
	switch {
	case pending > 0:
		return OnCallStatusPending
	case failed == 0:
		return OnCallStatusSubmitted
	case submitted == 0:
		return OnCallStatusFailed
	default:
		return OnCallStatusPartial
	}
}

// OnCallSubmission is the per-plugin attempt to push an OnCallDoc to a
// remote system. A successful row is final — retry skips it to avoid
// duplicating tickets. A failed row is re-dispatched on the next submit.
type OnCallSubmission struct {
	ID          int64      `json:"id"`
	DocID       int64      `json:"doc_id"`
	PluginName  string     `json:"plugin_name"`
	Status      string     `json:"status"` // pending|submitted|failed
	ExternalRef *string    `json:"external_ref,omitempty"`
	ExternalURL *string    `json:"external_url,omitempty"`
	LastError   *string    `json:"last_error,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	SubmittedAt *time.Time `json:"submitted_at,omitempty"`
}

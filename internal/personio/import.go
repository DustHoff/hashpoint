package personio

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dusthoff/hashpoint/internal/storage"
)

// FallbackTagName is the name of the auto-created tag that catches Personio
// periods whose project_id does not match any local tag's
// PersonioProjectID. The schema constrains tag names to `#[A-Za-z0-9]*` —
// hence the camel-case form. Created lazily on the first import that
// needs it. We flip SyncToPersonio off so a later sync of an imported
// block does not loop the period back to Personio under a different
// identity.
const FallbackTagName = "#PersonioImport"

// SyncPreflight is the result of peeking at a Personio day before sync. The
// frontend renders a confirm dialog from this whenever ExistingPeriods is
// non-empty, so the user can choose between override (current behaviour:
// PUT replaces the day) and import (pull the existing periods into local
// tag blocks). Trackable=false days are surfaced as well — the frontend
// shows them as not-syncable instead of asking the user to choose.
type SyncPreflight struct {
	Day              string            `json:"day"`               // YYYY-MM-DD (local)
	DayID            string            `json:"day_id"`            // empty when Personio has no record yet
	State            string            `json:"state"`             // "trackable", "locked", …
	Trackable        bool              `json:"trackable"`         // computed from State
	ExistingPeriods  []PreflightPeriod `json:"existing_periods"`  // type=work entries Personio has on the day
	LocalBlockCount  int               `json:"local_block_count"` // closed local tag blocks the override would push
	LocalDurationSec int64             `json:"local_duration_sec"`
}

// HasExistingPeriods reports whether Personio already has work-type periods
// on the day. The frontend uses this to decide whether to show the modal.
// Break periods are never returned in ExistingPeriods, so non-empty here
// means non-empty work entries.
func (p SyncPreflight) HasExistingPeriods() bool { return len(p.ExistingPeriods) > 0 }

// PreflightPeriod is the frontend-shaped view of one existing Personio
// period for the day under inspection. Times are passed through as
// Personio formats them (local-naive YYYY-MM-DDTHH:MM:SS) so the JS can
// render them without timezone gymnastics.
type PreflightPeriod struct {
	ID        string `json:"id"`
	Start     string `json:"start"`
	End       string `json:"end"`
	Type      string `json:"type"`
	Comment   string `json:"comment"`
	ProjectID string `json:"project_id,omitempty"`
	TagName   string `json:"tag_name,omitempty"` // local tag name resolved via personio_project_id, when available
}

// ImportResult reports the outcome of an Import run. PeriodsConsidered is
// the number of work-type periods returned by Personio; BlocksCreated is
// the number of local tag-block rows inserted (a single Personio period
// can become multiple blocks once existing local blocks have been
// subtracted).
type ImportResult struct {
	PeriodsConsidered int      `json:"periods_considered"`
	BlocksCreated     int      `json:"blocks_created"`
	PeriodsSkipped    int      `json:"periods_skipped"`
	FallbackTagUsed   bool     `json:"fallback_tag_used"`
	Errors            []string `json:"errors,omitempty"`
}

// Preflight peeks at the Personio attendance day for `day` (interpreted as
// the local calendar date). Returns the day's existing periods and state
// so the caller can decide whether to override (PUT) or import. This call
// only reads — no writes, local or remote.
func (s *Syncer) Preflight(ctx context.Context, day time.Time) (*SyncPreflight, error) {
	if s.client == nil {
		return nil, errors.New("personio: client not configured")
	}
	employeeID, err := s.ensureEmployeeID(ctx)
	if err != nil {
		return nil, err
	}

	midnight := localMidnight(day)
	dateStr := midnight.Format("2006-01-02")

	timecards, err := s.client.FetchTimesheet(ctx, employeeID, midnight, midnight)
	if err != nil {
		return nil, fmt.Errorf("timesheet: %w", err)
	}
	var tc Timecard
	for _, t := range timecards {
		if t.Date == dateStr {
			tc = t
			break
		}
	}

	tags, err := s.tags.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tags: %w", err)
	}
	tagsByProject := make(map[string]storage.Tag, len(tags))
	for _, t := range tags {
		if t.PersonioProjectID != nil && strings.TrimSpace(*t.PersonioProjectID) != "" {
			tagsByProject[strings.TrimSpace(*t.PersonioProjectID)] = t
		}
	}

	// ExistingPeriods is preallocated as an empty slice (not nil) so the
	// JSON payload sent to the frontend always carries `[]` instead of
	// `null` — the TS interface declares it as a non-nullable array, and
	// `.length` access would otherwise crash on Personio-clean days.
	out := &SyncPreflight{
		Day:             dateStr,
		DayID:           strings.TrimSpace(tc.DayID),
		State:           tc.State,
		Trackable:       tc.Trackable(),
		ExistingPeriods: []PreflightPeriod{},
	}
	for _, p := range tc.Periods {
		if !strings.EqualFold(p.Type, "work") {
			continue
		}
		pf := PreflightPeriod{
			ID:      p.ID,
			Start:   p.Start,
			End:     p.End,
			Type:    p.Type,
			Comment: p.Comment,
		}
		if p.ProjectID != nil {
			pf.ProjectID = strconv.FormatInt(*p.ProjectID, 10)
			if t, ok := tagsByProject[pf.ProjectID]; ok {
				pf.TagName = t.Name
			}
		}
		out.ExistingPeriods = append(out.ExistingPeriods, pf)
	}

	fromUTC := midnight.UTC()
	toUTC := midnight.Add(24 * time.Hour).UTC()
	localBlocks, err := s.blocks.ListBetween(ctx, fromUTC, toUTC)
	if err != nil {
		return nil, fmt.Errorf("list local blocks: %w", err)
	}
	for _, b := range localBlocks {
		if b.EndTime == nil {
			continue
		}
		out.LocalBlockCount++
		out.LocalDurationSec += b.DurationSec
	}
	return out, nil
}

// ImportDay pulls the existing work-type periods from Personio and writes
// them to the local tag-block table for `day`. Existing local tag blocks
// are authoritative: each Personio period is trimmed against them and the
// remaining sub-ranges (if any) are inserted as new manual tag blocks.
//
// Imported blocks carry the Personio period's comment as their description
// and a tag resolved via personio_project_id. Periods without a matching
// tag fall back to the auto-created `FallbackTagName` tag so no time
// silently disappears.
//
// Imported blocks are inserted as `is_manual=true` so the auto-tagging
// engine never reclaims them. They are NOT marked synced — re-syncing the
// day after import will push them back to Personio if their tag has
// SyncToPersonio enabled.
func (s *Syncer) ImportDay(ctx context.Context, day time.Time) (*ImportResult, error) {
	if s.client == nil {
		return nil, errors.New("personio: client not configured")
	}
	employeeID, err := s.ensureEmployeeID(ctx)
	if err != nil {
		return nil, err
	}

	midnight := localMidnight(day)
	dateStr := midnight.Format("2006-01-02")

	timecards, err := s.client.FetchTimesheet(ctx, employeeID, midnight, midnight)
	if err != nil {
		return nil, fmt.Errorf("timesheet: %w", err)
	}
	var tc Timecard
	for _, t := range timecards {
		if t.Date == dateStr {
			tc = t
			break
		}
	}

	tags, err := s.tags.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tags: %w", err)
	}
	tagsByProject := make(map[string]int64, len(tags))
	for _, t := range tags {
		if t.PersonioProjectID != nil && strings.TrimSpace(*t.PersonioProjectID) != "" {
			tagsByProject[strings.TrimSpace(*t.PersonioProjectID)] = t.ID
		}
	}

	fromUTC := midnight.UTC()
	toUTC := midnight.Add(24 * time.Hour).UTC()
	localBlocks, err := s.blocks.ListBetween(ctx, fromUTC, toUTC)
	if err != nil {
		return nil, fmt.Errorf("list local blocks: %w", err)
	}
	existing := make([]timeRange, 0, len(localBlocks))
	for _, b := range localBlocks {
		if b.EndTime == nil {
			continue
		}
		existing = append(existing, timeRange{Start: b.StartTime.UTC(), End: b.EndTime.UTC()})
	}

	res := &ImportResult{}
	var fallbackTagID int64
	for _, p := range tc.Periods {
		res.PeriodsConsidered++
		if !strings.EqualFold(p.Type, "work") {
			res.PeriodsSkipped++
			continue
		}
		ps, err := time.ParseInLocation("2006-01-02T15:04:05", p.Start, time.Local)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("period %s: parse start %q: %v", p.ID, p.Start, err))
			res.PeriodsSkipped++
			continue
		}
		pe, err := time.ParseInLocation("2006-01-02T15:04:05", p.End, time.Local)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("period %s: parse end %q: %v", p.ID, p.End, err))
			res.PeriodsSkipped++
			continue
		}
		psUTC := ps.UTC()
		peUTC := pe.UTC()
		if !peUTC.After(psUTC) {
			res.PeriodsSkipped++
			continue
		}

		var tagID int64
		if p.ProjectID != nil {
			pid := strconv.FormatInt(*p.ProjectID, 10)
			if id, ok := tagsByProject[pid]; ok {
				tagID = id
			}
		}
		if tagID == 0 {
			if fallbackTagID == 0 {
				id, err := ensureFallbackTag(ctx, s.tags)
				if err != nil {
					res.Errors = append(res.Errors, fmt.Sprintf("period %s: ensure fallback tag: %v", p.ID, err))
					res.PeriodsSkipped++
					continue
				}
				fallbackTagID = id
			}
			tagID = fallbackTagID
			res.FallbackTagUsed = true
		}

		sub := subtractRanges(timeRange{Start: psUTC, End: peUTC}, existing)
		if len(sub) == 0 {
			res.PeriodsSkipped++
			continue
		}

		var desc *string
		if c := strings.TrimSpace(p.Comment); c != "" {
			desc = &c
		}
		inserted := 0
		for _, r := range sub {
			block := &storage.TagBlock{
				TagID:       tagID,
				Description: desc,
				StartTime:   r.Start,
				EndTime:     &r.End,
				DurationSec: int64(r.End.Sub(r.Start).Round(time.Second).Seconds()),
				IsManual:    true,
			}
			if err := s.blocks.Open(ctx, block); err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("period %s: insert: %v", p.ID, err))
				continue
			}
			res.BlocksCreated++
			inserted++
			// Append the freshly-inserted block to `existing` so subsequent
			// periods don't re-cover the same ground.
			existing = append(existing, r)
		}
		if inserted == 0 {
			res.PeriodsSkipped++
		}
	}
	return res, nil
}

func (s *Syncer) ensureEmployeeID(ctx context.Context) (int64, error) {
	if s.client.Session.EmployeeID != 0 {
		return s.client.Session.EmployeeID, nil
	}
	fetched, err := s.client.FetchEmployeeID(ctx)
	if err != nil {
		return 0, fmt.Errorf("fetch employee id: %w", err)
	}
	s.client.Session.EmployeeID = fetched
	return fetched, nil
}

// localMidnight returns 00:00 of `day`'s calendar date in time.Local. Used
// to label timesheet queries: Personio answers "what's on this calendar
// day?" and we want the day the user sees in the UI.
func localMidnight(day time.Time) time.Time {
	l := day.In(time.Local)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, time.Local)
}

// timeRange is a half-open [Start, End) interval used by subtractRanges.
type timeRange struct {
	Start, End time.Time
}

// subtractRanges returns r minus each range in `others`, as a list of
// disjoint sub-ranges within r. Output preserves chronological order.
//
// Two ranges overlap iff a.End > b.Start AND b.End > a.Start. Any input
// range that fully covers r yields an empty result. Inputs do not need
// to be pre-sorted; subtractRanges sorts a copy by start time.
func subtractRanges(r timeRange, others []timeRange) []timeRange {
	if !r.End.After(r.Start) {
		return nil
	}
	sorted := append([]timeRange(nil), others...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Start.Before(sorted[j].Start)
	})
	out := []timeRange{r}
	for _, o := range sorted {
		if !o.End.After(o.Start) {
			continue
		}
		var next []timeRange
		for _, x := range out {
			if !x.End.After(o.Start) || !o.End.After(x.Start) {
				next = append(next, x)
				continue
			}
			if x.Start.Before(o.Start) {
				next = append(next, timeRange{Start: x.Start, End: o.Start})
			}
			if x.End.After(o.End) {
				next = append(next, timeRange{Start: o.End, End: x.End})
			}
		}
		out = next
	}
	return out
}

// ensureFallbackTag returns the ID of the auto-import placeholder tag,
// creating it on first use. The tag is keyed by name (top-level) so it
// can be hand-edited in the tag manager without losing the import target.
func ensureFallbackTag(ctx context.Context, tags storage.TagRepository) (int64, error) {
	existing, err := tags.List(ctx)
	if err != nil {
		return 0, err
	}
	for _, t := range existing {
		if t.ParentID == nil && t.Name == FallbackTagName {
			return t.ID, nil
		}
	}
	color := "#94a3b8"
	t := &storage.Tag{
		Name:           FallbackTagName,
		Color:          &color,
		SyncToPersonio: false,
	}
	if err := tags.Create(ctx, t); err != nil {
		return 0, fmt.Errorf("create fallback tag: %w", err)
	}
	return t.ID, nil
}

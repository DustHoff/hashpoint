package personio

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/onesi/hashpoint/internal/storage"
	"github.com/onesi/hashpoint/internal/tagging"
)

// Syncer aggregates tagged blocks and pushes them to Personio.
type Syncer struct {
	client *Client
	blocks storage.FocusBlockRepository
	tags   storage.TagRepository
	logger *slog.Logger
	clock  func() time.Time
}

// NewSyncer wires a Syncer.
func NewSyncer(client *Client, blocks storage.FocusBlockRepository, tags storage.TagRepository, logger *slog.Logger) *Syncer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Syncer{
		client: client,
		blocks: blocks,
		tags:   tags,
		logger: logger,
		clock:  func() time.Time { return time.Now().UTC() },
	}
}

// Result reports the outcome of a sync run.
type Result struct {
	Periods         int
	BlocksProcessed int
	BlocksSkipped   int
	Errors          []string
}

// SyncDay syncs a single calendar day (in UTC).
func (s *Syncer) SyncDay(ctx context.Context, day time.Time) (*Result, error) {
	from := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	return s.SyncRange(ctx, from, to)
}

// SyncRange syncs all tagged blocks in [from, to).
func (s *Syncer) SyncRange(ctx context.Context, from, to time.Time) (*Result, error) {
	blocks, err := s.blocks.ListBetween(ctx, from, to)
	if err != nil {
		return nil, fmt.Errorf("list blocks: %w", err)
	}
	tags, err := s.tags.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tags: %w", err)
	}
	tagsByID := make(map[int64]storage.Tag, len(tags))
	for _, t := range tags {
		tagsByID[t.ID] = t
	}

	periods := buildPeriods(blocks, tagsByID)
	res := &Result{}
	for _, p := range periods {
		if err := s.pushPeriod(ctx, p); err != nil {
			res.Errors = append(res.Errors, err.Error())
			continue
		}
		res.Periods++
		res.BlocksProcessed += len(p.BlockIDs)
	}
	for _, b := range blocks {
		if shouldSkip(b, tagsByID) {
			res.BlocksSkipped++
		}
	}
	return res, nil
}

func shouldSkip(b storage.FocusBlock, tags map[int64]storage.Tag) bool {
	if b.IsIdle || b.TagID == nil || b.EndTime == nil {
		return true
	}
	tag, ok := tags[*b.TagID]
	if !ok {
		return true
	}
	mapping := tagging.Resolve(tag, tags)
	return !mapping.SyncToPersonio || mapping.ProjectID == ""
}

// Period represents one aggregated attendance entry.
type Period struct {
	Date       time.Time
	Start      time.Time
	End        time.Time
	ProjectID  string
	ActivityID string
	Comments   []string
	BlockIDs   []int64
}

// buildPeriods groups blocks by (date, project_id, activity_id) and computes
// the contiguous start/end time of each group plus deduplicated comments.
func buildPeriods(blocks []storage.FocusBlock, tags map[int64]storage.Tag) []Period {
	type key struct {
		date       string
		projectID  string
		activityID string
	}
	type bucket struct {
		blocks   []storage.FocusBlock
		comments []string
		seen     map[string]struct{}
	}
	groups := make(map[key]*bucket)

	for _, b := range blocks {
		if shouldSkip(b, tags) {
			continue
		}
		tag := tags[*b.TagID]
		m := tagging.Resolve(tag, tags)
		k := key{
			date:       b.StartTime.UTC().Format("2006-01-02"),
			projectID:  m.ProjectID,
			activityID: m.ActivityID,
		}
		bk, ok := groups[k]
		if !ok {
			bk = &bucket{seen: map[string]struct{}{}}
			groups[k] = bk
		}
		bk.blocks = append(bk.blocks, b)
		c := m.BuildComment()
		if b.Description != nil {
			if d := strings.TrimSpace(*b.Description); d != "" {
				if c == "" {
					c = d
				} else {
					c = c + " — " + d
				}
			}
		}
		if c != "" {
			if _, dup := bk.seen[c]; !dup {
				bk.seen[c] = struct{}{}
				bk.comments = append(bk.comments, c)
			}
		}
	}

	out := make([]Period, 0, len(groups))
	for k, bk := range groups {
		date, _ := time.Parse("2006-01-02", k.date)
		start, end := blockSpan(bk.blocks)
		ids := make([]int64, 0, len(bk.blocks))
		for _, b := range bk.blocks {
			ids = append(ids, b.ID)
		}
		out = append(out, Period{
			Date:       date,
			Start:      start,
			End:        end,
			ProjectID:  k.projectID,
			ActivityID: k.activityID,
			Comments:   bk.comments,
			BlockIDs:   ids,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Date.Equal(out[j].Date) {
			return out[i].Date.Before(out[j].Date)
		}
		return out[i].Start.Before(out[j].Start)
	})
	return out
}

func blockSpan(blocks []storage.FocusBlock) (time.Time, time.Time) {
	if len(blocks) == 0 {
		return time.Time{}, time.Time{}
	}
	start := blocks[0].StartTime
	var end time.Time
	for _, b := range blocks {
		if b.StartTime.Before(start) {
			start = b.StartTime
		}
		if b.EndTime != nil && b.EndTime.After(end) {
			end = *b.EndTime
		}
	}
	return start.UTC(), end.UTC()
}

func (s *Syncer) pushPeriod(ctx context.Context, p Period) error {
	if s.client == nil {
		return errors.New("personio client not configured")
	}
	a := AttendanceCreate{
		EmployeeID: s.client.EmployeeID,
		Date:       p.Date.Format("2006-01-02"),
		StartTime:  p.Start.Local().Format("15:04"),
		EndTime:    p.End.Local().Format("15:04"),
		Comment:    strings.Join(p.Comments, "; "),
		ProjectID:  p.ProjectID,
		ActivityID: p.ActivityID,
	}

	res, err := s.client.CreateAttendance(ctx, a)
	if err != nil {
		return fmt.Errorf("create attendance for %s: %w", a.Date, err)
	}
	now := s.clock()
	for _, id := range p.BlockIDs {
		if err := s.blocks.MarkSynced(ctx, id, res.ID, now); err != nil {
			s.logger.Warn("mark synced failed", "block_id", id, "err", err)
		}
	}
	return nil
}

package personio

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/onesi/hashpoint/internal/storage"
	"github.com/onesi/hashpoint/internal/tagging"
)

// Syncer aggregates tagged blocks and pushes them to Personio via the
// internal/UI API.
type Syncer struct {
	client *UIClient
	blocks storage.FocusBlockRepository
	tags   storage.TagRepository
	logger *slog.Logger
	clock  func() time.Time
}

// NewSyncer wires a Syncer.
func NewSyncer(client *UIClient, blocks storage.FocusBlockRepository, tags storage.TagRepository, logger *slog.Logger) *Syncer {
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
	if s.client == nil {
		return nil, errors.New("personio: client not configured")
	}
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

	periodsByDay := buildDayPeriods(blocks, tagsByID)
	res := &Result{}
	for _, b := range blocks {
		if shouldSkip(b, tagsByID) {
			res.BlocksSkipped++
		}
	}
	if len(periodsByDay) == 0 {
		return res, nil
	}

	employeeID := s.client.Session.EmployeeID
	if employeeID == 0 {
		fetched, err := s.client.FetchEmployeeID(ctx)
		if err != nil {
			return res, fmt.Errorf("fetch employee id: %w", err)
		}
		s.client.Session.EmployeeID = fetched
		employeeID = fetched
	}

	// Sorted day list so calendar lookups are deterministic.
	days := make([]string, 0, len(periodsByDay))
	for d := range periodsByDay {
		days = append(days, d)
	}
	sort.Strings(days)

	earliest, _ := time.ParseInLocation("2006-01-02", days[0], time.Local)
	latest, _ := time.ParseInLocation("2006-01-02", days[len(days)-1], time.Local)
	timecards, err := s.client.FetchTimesheet(ctx, employeeID, earliest, latest)
	if err != nil {
		return res, fmt.Errorf("timesheet: %w", err)
	}
	tcByDate := make(map[string]Timecard, len(timecards))
	for _, t := range timecards {
		tcByDate[t.Date] = t
	}

	for _, dateStr := range days {
		dayPayload := periodsByDay[dateStr]
		tc, ok := tcByDate[dateStr]
		if !ok {
			res.Errors = append(res.Errors, fmt.Sprintf("%s: kein Timesheet-Eintrag — Personio betrachtet diesen Tag als nicht buchbar", dateStr))
			continue
		}
		if !tc.Trackable() {
			res.Errors = append(res.Errors, fmt.Sprintf("%s: Tag ist in Personio %q und kann nicht beschrieben werden", dateStr, tc.State))
			continue
		}
		dayID := strings.TrimSpace(tc.DayID)
		if dayID == "" {
			// Day has no Personio record yet — generate a fresh UUID; the
			// PUT endpoint accepts that as an upsert key.
			dayID = newUUIDv4()
		}
		payload := SetDayPayload{
			EmployeeID:      employeeID,
			Periods:         dayPayload.list,
			OriginalPeriods: dayPayload.list,
			Geolocation:     nil,
			IsFromClockOut:  false,
		}
		if err := s.client.SetDay(ctx, dayID, payload); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("%s: %s", dateStr, err.Error()))
			continue
		}
		res.Periods += len(payload.Periods)
		res.BlocksProcessed += len(dayPayload.blockIDs)
		now := s.clock()
		for _, id := range dayPayload.blockIDs {
			if err := s.blocks.MarkSynced(ctx, id, dayID, now); err != nil {
				s.logger.Warn("mark synced failed", "block_id", id, "err", err)
			}
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

// dayPeriods is the per-date bucket built from tag-grouped blocks.
type dayPeriods struct {
	list     []Period
	blockIDs []int64
}

// buildDayPeriods groups blocks by (local-date, project_id, comment) and
// produces one Period per group with start/end spanning the contained
// blocks. Times are formatted as local-naive ISO (YYYY-MM-DDTHH:MM:SS) — the
// shape Personio's UI sends and expects.
func buildDayPeriods(blocks []storage.FocusBlock, tags map[int64]storage.Tag) map[string]*dayPeriods {
	type key struct {
		date    string
		project string
		comment string
	}
	type bucket struct {
		blocks  []storage.FocusBlock
		project string
		comment string
	}
	groups := make(map[string]map[key]*bucket)

	for _, b := range blocks {
		if shouldSkip(b, tags) {
			continue
		}
		tag := tags[*b.TagID]
		m := tagging.Resolve(tag, tags)
		date := b.StartTime.Local().Format("2006-01-02")
		c := buildComment(m, b)
		k := key{date: date, project: m.ProjectID, comment: c}
		byKey := groups[date]
		if byKey == nil {
			byKey = make(map[key]*bucket)
			groups[date] = byKey
		}
		bk, ok := byKey[k]
		if !ok {
			bk = &bucket{project: m.ProjectID, comment: c}
			byKey[k] = bk
		}
		bk.blocks = append(bk.blocks, b)
	}

	out := make(map[string]*dayPeriods, len(groups))
	for date, byKey := range groups {
		dp := &dayPeriods{}
		for _, bk := range byKey {
			start, end := blockSpan(bk.blocks)
			period := Period{
				ID:            newUUIDv4(),
				Comment:       bk.comment,
				PeriodType:    "work",
				Start:         start.Local().Format("2006-01-02T15:04:05"),
				End:           end.Local().Format("2006-01-02T15:04:05"),
				AutoGenerated: false,
			}
			if pid, err := strconv.ParseInt(strings.TrimSpace(bk.project), 10, 64); err == nil && pid != 0 {
				period.ProjectID = &pid
			}
			dp.list = append(dp.list, period)
			for _, b := range bk.blocks {
				dp.blockIDs = append(dp.blockIDs, b.ID)
			}
		}
		sort.SliceStable(dp.list, func(i, j int) bool {
			return dp.list[i].Start < dp.list[j].Start
		})
		out[date] = dp
	}
	return out
}

func buildComment(m tagging.EffectiveMapping, b storage.FocusBlock) string {
	c := m.BuildComment()
	if b.Description != nil {
		if d := strings.TrimSpace(*b.Description); d != "" {
			if c == "" {
				return d
			}
			return c + " — " + d
		}
	}
	return c
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

// newUUIDv4 returns a fresh RFC 4122 v4 UUID. Personio's PUT day endpoint
// accepts any well-formed UUID and creates a new record if it does not yet
// exist, so we use this both for fresh days and for fresh periods.
func newUUIDv4() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]),
		hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]),
		hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:16]),
	)
}

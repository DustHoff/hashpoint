package personio

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onesi/hashpoint/internal/storage"
)

// importTestEnv wires a Syncer with an in-memory SQLite DB and a stubbed
// Personio HTTP server so the import/preflight flows can be exercised
// end-to-end without touching the real API.
type importTestEnv struct {
	t      *testing.T
	ctx    context.Context
	syncer *Syncer
	tags   *storage.TagRepo
	blocks *storage.TagBlockRepo
	srv    *httptest.Server
	resp   *timesheetStub
}

// timesheetStub holds the canned timecard the test server returns. Tests
// mutate this between requests to control what Personio "has".
type timesheetStub struct {
	Date    string
	State   string
	DayID   string
	Periods []map[string]any
}

func newImportEnv(t *testing.T) *importTestEnv {
	t.Helper()
	ctx := context.Background()
	db, err := storage.OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	tags := storage.NewTagRepo(db)
	blocks := storage.NewTagBlockRepo(db)

	stub := &timesheetStub{Date: "2026-05-08", State: "trackable", DayID: "day-uuid"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/svc/attendance-bff/v1/timesheet/"):
			tc := map[string]any{
				"date":       stub.Date,
				"state":      stub.State,
				"day_id":     stub.DayID,
				"is_off_day": false,
				"periods":    stub.Periods,
			}
			payload := map[string]any{
				"timecards": []any{tc},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(payload)
		case r.URL.Path == "/api/v1/navigation/context":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":{"user":{"id":4242}}}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	client := &UIClient{
		BaseURL: srv.URL,
		Session: &Session{Tenant: "lmis", EmployeeID: 4242},
		http:    &http.Client{Timeout: 5 * time.Second},
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	syncer := NewSyncer(client, blocks, tags, slog.New(slog.NewTextHandler(io.Discard, nil)))

	return &importTestEnv{
		t: t, ctx: ctx, syncer: syncer, tags: tags, blocks: blocks, srv: srv, resp: stub,
	}
}

func TestSubtractRanges_NoOverlap(t *testing.T) {
	t.Parallel()
	r := timeRange{
		Start: time.Date(2026, 5, 8, 8, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC),
	}
	others := []timeRange{
		{
			Start: time.Date(2026, 5, 8, 14, 0, 0, 0, time.UTC),
			End:   time.Date(2026, 5, 8, 16, 0, 0, 0, time.UTC),
		},
	}
	got := subtractRanges(r, others)
	if len(got) != 1 || !got[0].Start.Equal(r.Start) || !got[0].End.Equal(r.End) {
		t.Fatalf("expected single range %v unchanged, got %v", r, got)
	}
}

func TestSubtractRanges_FullyCovered(t *testing.T) {
	t.Parallel()
	r := timeRange{
		Start: time.Date(2026, 5, 8, 9, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 5, 8, 11, 0, 0, 0, time.UTC),
	}
	others := []timeRange{
		{
			Start: time.Date(2026, 5, 8, 8, 0, 0, 0, time.UTC),
			End:   time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC),
		},
	}
	got := subtractRanges(r, others)
	if len(got) != 0 {
		t.Fatalf("expected empty result, got %v", got)
	}
}

func TestSubtractRanges_StrictlyInside(t *testing.T) {
	t.Parallel()
	r := timeRange{
		Start: time.Date(2026, 5, 8, 8, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC),
	}
	others := []timeRange{
		{
			Start: time.Date(2026, 5, 8, 9, 0, 0, 0, time.UTC),
			End:   time.Date(2026, 5, 8, 10, 0, 0, 0, time.UTC),
		},
	}
	got := subtractRanges(r, others)
	if len(got) != 2 {
		t.Fatalf("expected 2 ranges, got %v", got)
	}
	if !got[0].Start.Equal(r.Start) || !got[0].End.Equal(others[0].Start) {
		t.Errorf("left half wrong: %v", got[0])
	}
	if !got[1].Start.Equal(others[0].End) || !got[1].End.Equal(r.End) {
		t.Errorf("right half wrong: %v", got[1])
	}
}

func TestSubtractRanges_CrossingEdges(t *testing.T) {
	t.Parallel()
	r := timeRange{
		Start: time.Date(2026, 5, 8, 8, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC),
	}
	// One blocker covers the left edge; another covers the right edge.
	others := []timeRange{
		{
			Start: time.Date(2026, 5, 8, 7, 0, 0, 0, time.UTC),
			End:   time.Date(2026, 5, 8, 9, 0, 0, 0, time.UTC),
		},
		{
			Start: time.Date(2026, 5, 8, 11, 0, 0, 0, time.UTC),
			End:   time.Date(2026, 5, 8, 13, 0, 0, 0, time.UTC),
		},
	}
	got := subtractRanges(r, others)
	if len(got) != 1 {
		t.Fatalf("expected single middle range, got %v", got)
	}
	if !got[0].Start.Equal(others[0].End) || !got[0].End.Equal(others[1].Start) {
		t.Errorf("middle range wrong: %v", got[0])
	}
}

func TestSubtractRanges_EmptyOthers(t *testing.T) {
	t.Parallel()
	r := timeRange{
		Start: time.Date(2026, 5, 8, 8, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 5, 8, 9, 0, 0, 0, time.UTC),
	}
	got := subtractRanges(r, nil)
	if len(got) != 1 || !got[0].Start.Equal(r.Start) {
		t.Fatalf("expected unchanged range, got %v", got)
	}
}

func TestPreflight_NoExistingPeriods(t *testing.T) {
	t.Parallel()
	e := newImportEnv(t)
	e.resp.Periods = nil

	day, _ := time.ParseInLocation("2006-01-02", "2026-05-08", time.Local)
	pre, err := e.syncer.Preflight(e.ctx, day.UTC())
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if pre.HasExistingPeriods() {
		t.Errorf("expected no existing periods, got %v", pre.ExistingPeriods)
	}
	if !pre.Trackable {
		t.Errorf("expected trackable=true")
	}
	if pre.DayID != "day-uuid" {
		t.Errorf("expected day_id=day-uuid, got %q", pre.DayID)
	}
}

func TestPreflight_BreakPeriodsIgnored(t *testing.T) {
	t.Parallel()
	e := newImportEnv(t)
	e.resp.Periods = []map[string]any{
		{
			"id":         "br-1",
			"start":      "2026-05-08T12:00:00",
			"end":        "2026-05-08T12:30:00",
			"type":       "break",
			"comment":    "",
			"project_id": nil,
		},
	}
	day, _ := time.ParseInLocation("2006-01-02", "2026-05-08", time.Local)
	pre, err := e.syncer.Preflight(e.ctx, day.UTC())
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if pre.HasExistingPeriods() {
		t.Errorf("break-only days must not trigger conflict, got %v", pre.ExistingPeriods)
	}
}

func TestPreflight_ResolvesTagName(t *testing.T) {
	t.Parallel()
	e := newImportEnv(t)
	pid := "4711"
	tag := storage.Tag{Name: "#projekta", PersonioProjectID: &pid, SyncToPersonio: true}
	if err := e.tags.Create(e.ctx, &tag); err != nil {
		t.Fatalf("create tag: %v", err)
	}
	e.resp.Periods = []map[string]any{
		{
			"id":         "p-1",
			"start":      "2026-05-08T08:00:00",
			"end":        "2026-05-08T10:00:00",
			"type":       "work",
			"comment":    "Refactor",
			"project_id": 4711,
		},
	}
	day, _ := time.ParseInLocation("2006-01-02", "2026-05-08", time.Local)
	pre, err := e.syncer.Preflight(e.ctx, day.UTC())
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if len(pre.ExistingPeriods) != 1 {
		t.Fatalf("expected 1 period, got %v", pre.ExistingPeriods)
	}
	if pre.ExistingPeriods[0].TagName != "#projekta" {
		t.Errorf("expected resolved TagName=#projekta, got %q", pre.ExistingPeriods[0].TagName)
	}
	if pre.ExistingPeriods[0].ProjectID != "4711" {
		t.Errorf("expected ProjectID=4711, got %q", pre.ExistingPeriods[0].ProjectID)
	}
}

func TestImportDay_NoLocalConflict(t *testing.T) {
	t.Parallel()
	e := newImportEnv(t)
	pid := "4711"
	tag := storage.Tag{Name: "#projekta", PersonioProjectID: &pid, SyncToPersonio: true}
	if err := e.tags.Create(e.ctx, &tag); err != nil {
		t.Fatalf("create tag: %v", err)
	}
	// Personio has one work period 08:00-10:00 local.
	e.resp.Periods = []map[string]any{
		{
			"id":         "p-1",
			"start":      "2026-05-08T08:00:00",
			"end":        "2026-05-08T10:00:00",
			"type":       "work",
			"comment":    "Refactor",
			"project_id": 4711,
		},
	}
	day, _ := time.ParseInLocation("2006-01-02", "2026-05-08", time.Local)
	res, err := e.syncer.ImportDay(e.ctx, day.UTC())
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.BlocksCreated != 1 {
		t.Fatalf("expected 1 block created, got %+v", res)
	}
	if res.FallbackTagUsed {
		t.Errorf("matched tag should not trigger fallback")
	}
	// Verify the inserted block.
	got, err := e.blocks.ListBetween(e.ctx,
		time.Date(2026, 5, 7, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 5, 9, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("list blocks: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 block, got %d: %+v", len(got), got)
	}
	if got[0].TagID != tag.ID {
		t.Errorf("expected tag id %d, got %d", tag.ID, got[0].TagID)
	}
	if got[0].Description == nil || *got[0].Description != "Refactor" {
		t.Errorf("expected description 'Refactor', got %v", got[0].Description)
	}
	if !got[0].IsManual {
		t.Errorf("imported blocks must be manual to survive auto-tagging")
	}
}

func TestImportDay_TrimsAroundLocalBlock(t *testing.T) {
	t.Parallel()
	e := newImportEnv(t)
	pid := "4711"
	tag := storage.Tag{Name: "#projekta", PersonioProjectID: &pid, SyncToPersonio: true}
	if err := e.tags.Create(e.ctx, &tag); err != nil {
		t.Fatalf("create tag: %v", err)
	}
	// Local block sitting in the middle of the Personio period (UTC).
	// Personio period is 08:00-10:00 in local time. Convert local to UTC
	// for the local block so the overlap is correctly carved.
	localStart := time.Date(2026, 5, 8, 8, 30, 0, 0, time.Local).UTC()
	localEnd := time.Date(2026, 5, 8, 9, 30, 0, 0, time.Local).UTC()
	localBlock := &storage.TagBlock{
		TagID:       tag.ID,
		StartTime:   localStart,
		EndTime:     &localEnd,
		DurationSec: int64(localEnd.Sub(localStart).Seconds()),
		IsManual:    true,
	}
	if err := e.blocks.Open(e.ctx, localBlock); err != nil {
		t.Fatalf("seed local block: %v", err)
	}

	e.resp.Periods = []map[string]any{
		{
			"id":         "p-1",
			"start":      "2026-05-08T08:00:00",
			"end":        "2026-05-08T10:00:00",
			"type":       "work",
			"comment":    "Refactor",
			"project_id": 4711,
		},
	}
	day, _ := time.ParseInLocation("2006-01-02", "2026-05-08", time.Local)
	res, err := e.syncer.ImportDay(e.ctx, day.UTC())
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.BlocksCreated != 2 {
		t.Fatalf("expected 2 blocks (left + right of carve-out), got %+v", res)
	}
	got, err := e.blocks.ListBetween(e.ctx,
		time.Date(2026, 5, 7, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 5, 9, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("list blocks: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 blocks (1 local + 2 imported), got %d: %+v", len(got), got)
	}
}

func TestImportDay_FallbackTag(t *testing.T) {
	t.Parallel()
	e := newImportEnv(t)
	// No tag with personio_project_id 4711.
	e.resp.Periods = []map[string]any{
		{
			"id":         "p-1",
			"start":      "2026-05-08T08:00:00",
			"end":        "2026-05-08T10:00:00",
			"type":       "work",
			"comment":    "Unmappable project",
			"project_id": 9999,
		},
	}
	day, _ := time.ParseInLocation("2006-01-02", "2026-05-08", time.Local)
	res, err := e.syncer.ImportDay(e.ctx, day.UTC())
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if !res.FallbackTagUsed {
		t.Errorf("expected fallback tag to be used")
	}
	if res.BlocksCreated != 1 {
		t.Errorf("expected 1 block created, got %+v", res)
	}
	tags, err := e.tags.List(e.ctx)
	if err != nil {
		t.Fatalf("list tags: %v", err)
	}
	var found *storage.Tag
	for i := range tags {
		if tags[i].Name == FallbackTagName {
			found = &tags[i]
			break
		}
	}
	if found == nil {
		t.Fatal("fallback tag was not created")
	}
	if found.SyncToPersonio {
		t.Errorf("fallback tag must default to SyncToPersonio=false to avoid round-tripping imports")
	}
}

func TestImportDay_BreakSkipped(t *testing.T) {
	t.Parallel()
	e := newImportEnv(t)
	e.resp.Periods = []map[string]any{
		{
			"id":         "br-1",
			"start":      "2026-05-08T12:00:00",
			"end":        "2026-05-08T12:30:00",
			"type":       "break",
			"comment":    "",
			"project_id": nil,
		},
	}
	day, _ := time.ParseInLocation("2006-01-02", "2026-05-08", time.Local)
	res, err := e.syncer.ImportDay(e.ctx, day.UTC())
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.BlocksCreated != 0 {
		t.Errorf("breaks must not be imported: %+v", res)
	}
	if res.PeriodsSkipped != 1 {
		t.Errorf("expected 1 skipped, got %+v", res)
	}
}

func TestEnsureFallbackTag_Idempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := storage.OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	tags := storage.NewTagRepo(db)

	id1, err := ensureFallbackTag(ctx, tags)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	id2, err := ensureFallbackTag(ctx, tags)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if id1 != id2 {
		t.Errorf("expected idempotent tag id, got %d then %d", id1, id2)
	}
	// Ensure exactly one tag exists.
	all, _ := tags.List(ctx)
	count := 0
	for _, tag := range all {
		if tag.Name == FallbackTagName {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly one fallback tag, got %d", count)
	}
}

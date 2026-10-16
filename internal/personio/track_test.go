package personio

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dusthoff/hashpoint/internal/storage"
)

// TestBuildTrackPeriodsRequest pins the request shape against the one the
// Personio web app sends (HAR capture 2026-10-16, values anonymised).
func TestBuildTrackPeriodsRequest(t *testing.T) {
	t.Parallel()
	pid := int64(4711)
	day := time.Date(2026, 10, 13, 0, 0, 0, 0, time.Local)
	periods := []Period{
		{
			ID:      "1b05c1fb-d41d-46ce-892d-21ebce5cf5b6",
			Comment: "test",
			Start:   time.Date(2026, 10, 13, 1, 0, 0, 0, time.Local).UTC(),
			End:     time.Date(2026, 10, 13, 2, 0, 0, 0, time.Local).UTC(),
		},
		{
			ID:        "fa311279-ef48-4069-9e39-fce20aaf9010",
			Comment:   "mit Projekt",
			ProjectID: &pid,
			Start:     time.Date(2026, 10, 13, 2, 0, 30, 0, time.Local),
			End:       time.Date(2026, 10, 13, 3, 0, 30, 0, time.Local),
		},
	}
	raw, err := json.Marshal(buildTrackPeriodsRequest(4242, day, periods))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"query":"` + trackPeriodsQueryHash + `","operationName":"` + trackPeriodsOperation + `",` +
		`"variables":{"input":{"autoFix":false,"origin":"ATTENDANCE_ORIGIN_WEB",` +
		`"assumedAttributionDate":{"year":2026,"month":10,"day":13},"personId":{"id":"4242"},` +
		`"attendancePeriods":[` +
		`{"id":{"id":"1b05c1fb-d41d-46ce-892d-21ebce5cf5b6"},"type":"ATTENDANCE_PERIOD_TYPE_WORK",` +
		`"startLocalTime":{"date":{"year":2026,"month":10,"day":13},"time":{"hours":1,"minutes":0}},` +
		`"endLocalTime":{"date":{"year":2026,"month":10,"day":13},"time":{"hours":2,"minutes":0}},` +
		`"projectId":null,"comment":"test","personId":{"id":"4242"},"origin":"ATTENDANCE_ORIGIN_WEB",` +
		`"legacyBreakMinutes":0,"startGeolocation":null,"endGeolocation":null,"duration":{"seconds":"3600"}},` +
		`{"id":{"id":"fa311279-ef48-4069-9e39-fce20aaf9010"},"type":"ATTENDANCE_PERIOD_TYPE_WORK",` +
		`"startLocalTime":{"date":{"year":2026,"month":10,"day":13},"time":{"hours":2,"minutes":0,"seconds":30}},` +
		`"endLocalTime":{"date":{"year":2026,"month":10,"day":13},"time":{"hours":3,"minutes":0,"seconds":30}},` +
		`"projectId":{"id":"4711"},"comment":"mit Projekt","personId":{"id":"4242"},"origin":"ATTENDANCE_ORIGIN_WEB",` +
		`"legacyBreakMinutes":0,"startGeolocation":null,"endGeolocation":null,"duration":{"seconds":"3600"}}` +
		`]}}}`
	if string(raw) != want {
		t.Fatalf("request mismatch\n got: %s\nwant: %s", raw, want)
	}
}

func TestTrackPeriodsOnDay(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		status   int
		body     string
		wantErr  bool
		wantIs   error
		contains string
	}{
		{
			name:   "saved",
			status: http.StatusOK,
			body:   `{"data":{"trackAttendancePeriodsOnDay":{"saved":{"attendancePeriodsList":[{}]}}}}`,
		},
		{
			name:     "graphql validation error",
			status:   http.StatusOK,
			body:     `{"data":null,"errors":[{"message":"periods overlap"}]}`,
			wantErr:  true,
			contains: "periods overlap",
		},
		{
			name:    "persisted query unknown by code",
			status:  http.StatusOK,
			body:    `{"errors":[{"message":"not found","extensions":{"code":"PERSISTED_QUERY_NOT_FOUND"}}]}`,
			wantErr: true,
			wantIs:  ErrOperationOutdated,
		},
		{
			name:    "persisted query unknown by message",
			status:  http.StatusOK,
			body:    `{"errors":[{"message":"PersistedQueryNotFound"}]}`,
			wantErr: true,
			wantIs:  ErrOperationOutdated,
		},
		{
			name:     "missing saved block",
			status:   http.StatusOK,
			body:     `{"data":{"trackAttendancePeriodsOnDay":null}}`,
			wantErr:  true,
			contains: "ohne gespeicherte Perioden",
		},
		{
			name:     "http error",
			status:   http.StatusNotImplemented,
			body:     ``,
			wantErr:  true,
			contains: "status 501",
		},
		{
			name:    "session expired",
			status:  http.StatusUnauthorized,
			wantErr: true,
			wantIs:  ErrSessionExpired,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/graphql" || r.URL.Query().Get("op") != trackPeriodsOperation {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
				}
				if got := r.Header.Get("Content-Type"); got != "application/json" {
					t.Errorf("content-type = %q", got)
				}
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}))
			t.Cleanup(srv.Close)
			client := &UIClient{
				BaseURL: srv.URL,
				Session: &Session{Tenant: "lmis"},
				http:    &http.Client{Timeout: 5 * time.Second},
				logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
			}
			day := time.Date(2026, 10, 13, 0, 0, 0, 0, time.Local)
			err := client.TrackPeriodsOnDay(context.Background(), 4242, day, []Period{{
				ID:    "p-1",
				Start: day.Add(8 * time.Hour),
				End:   day.Add(9 * time.Hour),
			}})
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
				t.Fatalf("err = %v, want errors.Is %v", err, tt.wantIs)
			}
			if tt.contains != "" && !strings.Contains(err.Error(), tt.contains) {
				t.Fatalf("err = %q, want substring %q", err, tt.contains)
			}
		})
	}
}

func TestTrackPeriodsOnDay_ZeroEmployee(t *testing.T) {
	t.Parallel()
	c := &UIClient{}
	if err := c.TrackPeriodsOnDay(context.Background(), 0, time.Now(), nil); err == nil {
		t.Fatal("expected error for zero employee id")
	}
}

// TestSyncDay_WritesViaGraphQL exercises the full sync path: tag blocks are
// aggregated, sent as one GraphQL mutation per day, and marked synced with
// the timesheet's day_id.
func TestSyncDay_WritesViaGraphQL(t *testing.T) {
	t.Parallel()
	e := newImportEnv(t)
	pid := "4711"
	tag := storage.Tag{Name: "#projekta", PersonioProjectID: &pid, SyncToPersonio: true}
	if err := e.tags.Create(e.ctx, &tag); err != nil {
		t.Fatalf("create tag: %v", err)
	}
	start := time.Date(2026, 5, 8, 8, 0, 0, 0, time.Local).UTC()
	end := time.Date(2026, 5, 8, 10, 0, 0, 0, time.Local).UTC()
	block := &storage.TagBlock{
		TagID:       tag.ID,
		StartTime:   start,
		EndTime:     &end,
		DurationSec: int64(end.Sub(start).Seconds()),
		IsManual:    true,
	}
	if err := e.blocks.Open(e.ctx, block); err != nil {
		t.Fatalf("seed block: %v", err)
	}

	res, err := e.syncer.SyncDay(e.ctx, time.Date(2026, 5, 8, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("unexpected sync errors: %v", res.Errors)
	}
	if res.Periods != 1 || res.BlocksProcessed != 1 {
		t.Fatalf("result = %+v, want 1 period / 1 block", res)
	}

	reqs := e.resp.trackedRequests()
	if len(reqs) != 1 {
		t.Fatalf("got %d graphql requests, want 1", len(reqs))
	}
	in := reqs[0].Variables.Input
	if in.AssumedAttributionDate != (gqlDate{Year: 2026, Month: 5, Day: 8}) {
		t.Errorf("attribution date = %+v", in.AssumedAttributionDate)
	}
	if in.PersonID.ID != "4242" || len(in.AttendancePeriods) != 1 {
		t.Fatalf("input = %+v", in)
	}
	p := in.AttendancePeriods[0]
	if p.StartLocalTime.Time != (gqlTimeOfDay{Hours: 8}) || p.EndLocalTime.Time != (gqlTimeOfDay{Hours: 10}) {
		t.Errorf("times = %+v – %+v", p.StartLocalTime, p.EndLocalTime)
	}
	if p.ProjectID == nil || p.ProjectID.ID != "4711" {
		t.Errorf("project id = %+v", p.ProjectID)
	}
	if p.Duration == nil || p.Duration.Seconds != "7200" {
		t.Errorf("duration = %+v", p.Duration)
	}

	got, err := e.blocks.Get(e.ctx, block.ID)
	if err != nil {
		t.Fatalf("get block: %v", err)
	}
	if got.SyncedAt == nil || got.PersonioID == nil || *got.PersonioID != "day-uuid" {
		t.Fatalf("block not marked synced with day_id: %+v", got)
	}
}

func TestSyncDay_GraphQLErrorSurfacesPerDay(t *testing.T) {
	t.Parallel()
	e := newImportEnv(t)
	e.resp.TrackResp = `{"errors":[{"message":"PersistedQueryNotFound"}]}`
	tag := storage.Tag{Name: "#projekta", SyncToPersonio: true}
	if err := e.tags.Create(e.ctx, &tag); err != nil {
		t.Fatalf("create tag: %v", err)
	}
	start := time.Date(2026, 5, 8, 8, 0, 0, 0, time.Local).UTC()
	end := start.Add(time.Hour)
	block := &storage.TagBlock{TagID: tag.ID, StartTime: start, EndTime: &end, DurationSec: 3600, IsManual: true}
	if err := e.blocks.Open(e.ctx, block); err != nil {
		t.Fatalf("seed block: %v", err)
	}

	res, err := e.syncer.SyncDay(e.ctx, time.Date(2026, 5, 8, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(res.Errors) != 1 || !strings.Contains(res.Errors[0], "GraphQL-Operation unbekannt") {
		t.Fatalf("errors = %v", res.Errors)
	}
	got, err := e.blocks.Get(e.ctx, block.ID)
	if err != nil {
		t.Fatalf("get block: %v", err)
	}
	if got.SyncedAt != nil {
		t.Fatal("block must stay unsynced after a failed write")
	}
}

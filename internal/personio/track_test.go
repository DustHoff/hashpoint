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
	"sync"
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
	raw, err := json.Marshal(buildTrackPeriodsRequest(4242, day, periods, false))
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
			name:   "precondition reason from extensions",
			status: http.StatusOK,
			body: `{"data":null,"errors":[{"message":"FAILED_PRECONDITION","extensions":` +
				`{"title":"overlapping_periods","details":[{"index":0}]}}]}`,
			wantErr:  true,
			contains: `FAILED_PRECONDITION (overlapping_periods) [{"index":0}]`,
		},
		{
			name:   "precondition details without alerts drop period echo",
			status: http.StatusOK,
			body: `{"errors":[{"message":"FAILED_PRECONDITION","extensions":{"title":"something else",` +
				`"details":[{"validation":{"original":{"attendancePeriods":[{"hash":"x","duration":"11100s"}],"hint":"h"}}}]}}]}`,
			wantErr:  true,
			contains: `FAILED_PRECONDITION (something else) [{"validation":{"original":{"hint":"h"}}}]`,
		},
		{
			name:   "blocking policy alert is not auto-confirmed",
			status: http.StatusOK,
			body: `{"errors":[{"message":"FAILED_PRECONDITION","extensions":{"title":"Attendance periods violated blocking policy rules",` +
				`"details":[{"validation":{"original":{"attendancePeriods":[],` +
				`"alerts":[{"enforcement":"ENFORCEMENT_BLOCKING","date":{"year":2026,"month":10,"day":13},"maxDailyWork":{}}]}}}]}}]}`,
			wantErr:  true,
			contains: "Attendance periods violated blocking policy rules: maxDailyWork (BLOCKING)",
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
			_, err := client.TrackPeriodsOnDay(context.Background(), 4242, day, []Period{{
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
	if _, err := c.TrackPeriodsOnDay(context.Background(), 0, time.Now(), nil); err == nil {
		t.Fatal("expected error for zero employee id")
	}
}

// overtimeWarningError and overtimeSaved mirror the two responses of the
// web app's warning flow (HAR 2026-10-17, anonymised).
const (
	overtimeWarningError = `{"errors":[{"message":"FAILED_PRECONDITION","locations":[],"path":["trackAttendancePeriodsOnDay"],` +
		`"extensions":{"title":"Attendance periods violated blocking policy rules","details":[{"validation":{"original":{` +
		`"attendancePeriods":[{"hash":"x","type":"ATTENDANCE_PERIOD_TYPE_WORK","duration":"3600s"}],` +
		`"alerts":[{"enforcement":"ENFORCEMENT_WARNING","date":{"year":2026,"month":10,"day":13},` +
		`"overtimeApproaching":{"overtimeLimitMinutes":960,"currentOvertimeMinutes":720}}]}}}]}}]}`
	overtimeSaved = `{"data":{"trackAttendancePeriodsOnDay":{"original":{"attendancePeriodsList":[],"alertsList":[]},` +
		`"saved":{"attendancePeriodsList":[{}],"alertsList":[{"date":{"year":2026,"month":10,"day":13},` +
		`"enforcement":"ENFORCEMENT_WARNING","kind":{"__typename":"prototimeattendance_day_OvertimeApproachingAlert_v1",` +
		`"currentOvertimeMinutes":720,"overtimeLimitMinutes":960}}]}}}}`
)

// TestTrackPeriodsOnDay_ConfirmsWarnings reproduces the web app's flow: a
// warning-only rejection is resent with autoFix=true and the warning is
// returned to the caller.
func TestTrackPeriodsOnDay_ConfirmsWarnings(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var autoFixes []bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req trackPeriodsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode: %v", err)
		}
		mu.Lock()
		autoFixes = append(autoFixes, req.Variables.Input.AutoFix)
		mu.Unlock()
		if req.Variables.Input.AutoFix {
			_, _ = io.WriteString(w, overtimeSaved)
			return
		}
		_, _ = io.WriteString(w, overtimeWarningError)
	}))
	t.Cleanup(srv.Close)
	client := &UIClient{
		BaseURL: srv.URL,
		Session: &Session{Tenant: "lmis"},
		http:    &http.Client{Timeout: 5 * time.Second},
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	day := time.Date(2026, 10, 13, 0, 0, 0, 0, time.Local)
	alerts, err := client.TrackPeriodsOnDay(context.Background(), 4242, day, []Period{{ID: "p", Start: day.Add(8 * time.Hour), End: day.Add(9 * time.Hour)}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(autoFixes) != 2 || autoFixes[0] || !autoFixes[1] {
		t.Fatalf("autoFix sequence = %v, want [false true]", autoFixes)
	}
	if len(alerts) != 1 || !alerts[0].IsWarning() {
		t.Fatalf("alerts = %+v, want one warning", alerts)
	}
	if got, want := alerts[0].String(), "Überstunden nähern sich dem Limit (720 von 960 Min.)"; got != want {
		t.Fatalf("alert text = %q, want %q", got, want)
	}
}

func TestPolicyViolationError_OnlyWarnings(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		alerts []Alert
		want   bool
	}{
		{name: "no alerts", want: false},
		{name: "all warnings", alerts: []Alert{{Enforcement: enforcementWarning}, {Enforcement: enforcementWarning}}, want: true},
		{name: "mixed", alerts: []Alert{{Enforcement: enforcementWarning}, {Enforcement: "ENFORCEMENT_BLOCKING"}}, want: false},
		{name: "missing enforcement", alerts: []Alert{{}}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			pv := &PolicyViolationError{Alerts: tt.alerts}
			if got := pv.onlyWarnings(); got != tt.want {
				t.Fatalf("onlyWarnings = %v, want %v", got, tt.want)
			}
		})
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

func TestReuseExistingPeriodIDs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		existing string // JSON periods array of the timecard
		outgoing int
		want     []string
	}{
		{name: "no existing keeps fresh ids", existing: `[]`, outgoing: 2, want: []string{"new-0", "new-1"}},
		{
			name:     "more existing than outgoing — surplus dropped",
			existing: `[{"id":"b","start":"2026-10-13T02:00:00","type":"work"},{"id":"a","start":"2026-10-13T01:00:00","type":"work"}]`,
			outgoing: 1,
			want:     []string{"a"},
		},
		{
			name:     "fewer existing than outgoing — rest fresh",
			existing: `[{"id":"a","start":"2026-10-13T01:00:00","type":"work"}]`,
			outgoing: 3,
			want:     []string{"a", "new-1", "new-2"},
		},
		{
			name:     "breaks and empty ids are skipped",
			existing: `[{"id":"brk","start":"2026-10-13T00:30:00","type":"break"},{"id":"","start":"2026-10-13T00:45:00","type":"work"},{"id":"w","start":"2026-10-13T01:00:00","type":"WORK"}]`,
			outgoing: 2,
			want:     []string{"w", "new-1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var tc Timecard
			if err := json.Unmarshal([]byte(`{"periods":`+tt.existing+`}`), &tc); err != nil {
				t.Fatalf("unmarshal timecard: %v", err)
			}
			periods := make([]Period, tt.outgoing)
			for i := range periods {
				periods[i].ID = "new-" + string(rune('0'+i))
			}
			reuseExistingPeriodIDs(periods, tc)
			for i, p := range periods {
				if p.ID != tt.want[i] {
					t.Fatalf("period %d id = %q, want %q (all: %+v)", i, p.ID, tt.want[i], periods)
				}
			}
		})
	}
}

// TestSyncDay_OverwriteReusesExistingIDs ensures overwriting a populated day
// edits Personio's existing period instead of deleting all and recreating.
func TestSyncDay_OverwriteReusesExistingIDs(t *testing.T) {
	t.Parallel()
	e := newImportEnv(t)
	e.resp.Periods = []map[string]any{
		{"id": "existing-1", "start": "2026-05-08T01:00:00", "end": "2026-05-08T02:00:00", "type": "work", "comment": "test"},
	}
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
	if err != nil || len(res.Errors) != 0 {
		t.Fatalf("sync: err=%v errors=%v", err, res.Errors)
	}
	reqs := e.resp.trackedRequests()
	if len(reqs) != 1 || len(reqs[0].Variables.Input.AttendancePeriods) != 1 {
		t.Fatalf("unexpected requests: %+v", reqs)
	}
	if got := reqs[0].Variables.Input.AttendancePeriods[0].ID.ID; got != "existing-1" {
		t.Fatalf("period id = %q, want existing-1", got)
	}
}

func TestSyncDay_SurfacesPolicyWarnings(t *testing.T) {
	t.Parallel()
	e := newImportEnv(t)
	e.resp.TrackResp = overtimeSaved
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
	if err != nil || len(res.Errors) != 0 {
		t.Fatalf("sync: err=%v errors=%v", err, res.Errors)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "2026-05-08: Überstunden nähern sich dem Limit") {
		t.Fatalf("warnings = %v", res.Warnings)
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

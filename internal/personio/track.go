package personio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Personio migrated attendance writes from the REST endpoint
// PUT /svc/attendance-api/v1/days/{day_id} (now answers 501) to a GraphQL
// mutation. The web app sends it as a persisted query: the "query" field
// carries the SHA-256 of a server-side registered document instead of the
// GraphQL text, so operation name and hash must match what Personio has
// registered. Both are taken from the web client (HAR capture 2026-10-16)
// and change whenever Personio ships a new operation version.
const (
	trackPeriodsOperation = "TM_TrackAttendancePeriodsOnDay_v2026071301"
	trackPeriodsQueryHash = "52d040f7dfa51ff8a9a3cd149cd2da58c5d15ed404f2be69e1d081bcab879c83"

	attendanceOriginWeb  = "ATTENDANCE_ORIGIN_WEB"
	attendancePeriodWork = "ATTENDANCE_PERIOD_TYPE_WORK"
)

// ErrOperationOutdated indicates that Personio no longer knows the persisted
// GraphQL operation the client sends. It is fixed by updating
// trackPeriodsOperation / trackPeriodsQueryHash from a fresh web-app capture,
// not by re-authenticating.
var ErrOperationOutdated = errors.New("personio: GraphQL-Operation unbekannt — Personio hat die Web-API geändert, Hashpoint-Update nötig")

// Period is one attendance period of a day as built by the syncer. Start and
// End are wall-clock instants; they are rendered as local date + time of day
// when sent to Personio.
type Period struct {
	ID        string
	Comment   string
	ProjectID *int64
	Start     time.Time
	End       time.Time
}

// gqlID is Personio's GraphQL id wrapper: {"id":"<value>"}.
type gqlID struct {
	ID string `json:"id"`
}

type gqlDate struct {
	Year  int `json:"year"`
	Month int `json:"month"`
	Day   int `json:"day"`
}

type gqlTimeOfDay struct {
	Hours   int `json:"hours"`
	Minutes int `json:"minutes"`
	Seconds int `json:"seconds,omitempty"`
}

type gqlLocalTime struct {
	Date gqlDate      `json:"date"`
	Time gqlTimeOfDay `json:"time"`
}

// gqlDuration mirrors the web client, which sends int64 seconds as a
// JSON string (protobuf-JSON convention).
type gqlDuration struct {
	Seconds string `json:"seconds"`
}

type gqlAttendancePeriod struct {
	ID                 gqlID        `json:"id"`
	Type               string       `json:"type"`
	StartLocalTime     gqlLocalTime `json:"startLocalTime"`
	EndLocalTime       gqlLocalTime `json:"endLocalTime"`
	ProjectID          *gqlID       `json:"projectId"`
	Comment            string       `json:"comment"`
	PersonID           gqlID        `json:"personId"`
	Origin             string       `json:"origin"`
	LegacyBreakMinutes int          `json:"legacyBreakMinutes"`
	StartGeolocation   any          `json:"startGeolocation"`
	EndGeolocation     any          `json:"endGeolocation"`
	Duration           *gqlDuration `json:"duration"`
}

type trackPeriodsInput struct {
	AutoFix                bool                  `json:"autoFix"`
	Origin                 string                `json:"origin"`
	AssumedAttributionDate gqlDate               `json:"assumedAttributionDate"`
	PersonID               gqlID                 `json:"personId"`
	AttendancePeriods      []gqlAttendancePeriod `json:"attendancePeriods"`
}

type trackPeriodsRequest struct {
	Query         string `json:"query"`
	OperationName string `json:"operationName"`
	Variables     struct {
		Input trackPeriodsInput `json:"input"`
	} `json:"variables"`
}

// gqlError is one entry of a GraphQL errors[] array. Personio's gateway puts
// only the gRPC status name into Message ("FAILED_PRECONDITION",
// "INTERNAL", …); the actual reason lives in Extensions.Title and, for
// structured failures, Extensions.Details — the web app reads both.
type gqlError struct {
	Message    string `json:"message"`
	Extensions struct {
		Code    string          `json:"code"`
		Title   string          `json:"title"`
		Details json.RawMessage `json:"details"`
	} `json:"extensions"`
}

// String renders the error with its reason, e.g.
// "FAILED_PRECONDITION (overlapping_periods) [{…}]".
func (e gqlError) String() string {
	var b strings.Builder
	b.WriteString(e.Message)
	if t := strings.TrimSpace(e.Extensions.Title); t != "" && t != e.Message {
		b.WriteString(" (" + t + ")")
	}
	if d := compactDetails(e.Extensions.Details); d != "" {
		b.WriteString(" " + truncate(d, 800))
	}
	return truncate(b.String(), 1000)
}

// echoKeys are detail fields in which Personio echoes the submitted (and
// auto-fixed) periods back. They dwarf the actual rule violation, which
// would otherwise be truncated away, and repeat data the caller already has.
var echoKeys = map[string]bool{
	"attendancePeriods":     true,
	"attendancePeriodsList": true,
}

// compactDetails re-encodes extensions.details without the period echoes so
// the policy violation itself stays visible. Non-JSON or empty details yield
// the trimmed raw text or "".
func compactDetails(raw json.RawMessage) string {
	d := strings.TrimSpace(string(raw))
	if d == "" || d == "null" || d == "[]" || d == "{}" {
		return ""
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return d
	}
	out, err := json.Marshal(stripEchoes(v))
	if err != nil {
		return d
	}
	return string(out)
}

func stripEchoes(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if echoKeys[k] {
				delete(t, k)
				continue
			}
			t[k] = stripEchoes(val)
		}
		return t
	case []any:
		for i := range t {
			t[i] = stripEchoes(t[i])
		}
		return t
	default:
		return v
	}
}

const enforcementWarning = "ENFORCEMENT_WARNING"

// Alert is a time-policy alert Personio attaches to a day, e.g. approaching
// the overtime limit. Only alerts with Enforcement ENFORCEMENT_WARNING can be
// confirmed; anything else blocks the write.
type Alert struct {
	Enforcement string
	Kind        string                     // e.g. "overtimeApproaching"
	Fields      map[string]json.RawMessage // kind-specific payload
}

// IsWarning reports whether the alert is a confirmable warning.
func (a Alert) IsWarning() bool { return a.Enforcement == enforcementWarning }

// String renders the alert for the sync banner.
func (a Alert) String() string {
	if a.Kind == "overtimeApproaching" || a.Kind == "OvertimeApproachingAlert" {
		var cur, lim int64
		if json.Unmarshal(a.Fields["currentOvertimeMinutes"], &cur) == nil &&
			json.Unmarshal(a.Fields["overtimeLimitMinutes"], &lim) == nil {
			return fmt.Sprintf("Überstunden nähern sich dem Limit (%d von %d Min.)", cur, lim)
		}
	}
	kind := a.Kind
	if kind == "" {
		kind = "unbekannte Regel"
	}
	return fmt.Sprintf("%s (%s)", kind, strings.TrimPrefix(a.Enforcement, "ENFORCEMENT_"))
}

// parseErrorAlert reads an alert from FAILED_PRECONDITION details, where the
// kind is a sibling key of enforcement/date: {"enforcement":…,"date":…,
// "overtimeApproaching":{…}}.
func parseErrorAlert(m map[string]json.RawMessage) Alert {
	a := Alert{Enforcement: jsonString(m["enforcement"])}
	for k, v := range m {
		if k == "enforcement" || k == "date" {
			continue
		}
		a.Kind = k
		a.Fields = jsonObject(v)
		break
	}
	return a
}

// parseSavedAlert reads an alert from a successful response, where the kind
// is wrapped: {"enforcement":…,"kind":{"__typename":"…_OvertimeApproachingAlert_v1",…}}.
func parseSavedAlert(m map[string]json.RawMessage) Alert {
	a := Alert{Enforcement: jsonString(m["enforcement"]), Fields: jsonObject(m["kind"])}
	if typename := jsonString(a.Fields["__typename"]); typename != "" {
		// "prototimeattendance_day_OvertimeApproachingAlert_v1" → "OvertimeApproachingAlert"
		parts := strings.Split(typename, "_")
		if len(parts) >= 2 {
			typename = parts[len(parts)-2]
		}
		a.Kind = typename
	}
	return a
}

// jsonString decodes a JSON string; absent or non-string values yield ""
// (an alert without enforcement is then treated as blocking).
func jsonString(raw json.RawMessage) string {
	var s string
	if len(raw) == 0 || json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return s
}

// jsonObject decodes a JSON object; absent or non-object values yield nil.
func jsonObject(raw json.RawMessage) map[string]json.RawMessage {
	var m map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}

// PolicyViolationError is returned when Personio rejects a day because of
// time-policy alerts (FAILED_PRECONDITION with validation alerts).
type PolicyViolationError struct {
	Title  string
	Alerts []Alert
}

func (e *PolicyViolationError) Error() string {
	parts := make([]string, 0, len(e.Alerts))
	for _, a := range e.Alerts {
		parts = append(parts, a.String())
	}
	return fmt.Sprintf("personio: track periods: %s: %s", e.Title, strings.Join(parts, "; "))
}

// onlyWarnings reports whether every alert is confirmable — the case in
// which the web app asks the user and resends with autoFix=true.
func (e *PolicyViolationError) onlyWarnings() bool {
	if len(e.Alerts) == 0 {
		return false
	}
	for _, a := range e.Alerts {
		if !a.IsWarning() {
			return false
		}
	}
	return true
}

type trackPeriodsResponse struct {
	Data *struct {
		TrackAttendancePeriodsOnDay *struct {
			Saved *struct {
				AttendancePeriodsList []json.RawMessage            `json:"attendancePeriodsList"`
				AlertsList            []map[string]json.RawMessage `json:"alertsList"`
			} `json:"saved"`
		} `json:"trackAttendancePeriodsOnDay"`
	} `json:"data"`
	Errors []gqlError `json:"errors"`
}

// TrackPeriodsOnDay replaces all attendance periods of the local calendar
// day `day` with `periods`, using the same GraphQL mutation as the Personio
// web app. The day is addressed by date — Personio no longer needs a day_id
// for writes.
//
// Like the web app it first sends autoFix=false. If Personio rejects the day
// solely because of policy warnings (e.g. overtime approaching), the request
// is repeated with autoFix=true — the web app does the same once the user
// confirms the warning dialog; the only change autoFix was observed to make
// is filling gaps with break periods. The returned alerts are the warnings
// attached to the saved day so callers can surface them. Blocking alerts
// yield a *PolicyViolationError.
func (c *UIClient) TrackPeriodsOnDay(ctx context.Context, employeeID int64, day time.Time, periods []Period) ([]Alert, error) {
	if employeeID == 0 {
		return nil, errors.New("personio: employee id is zero")
	}
	alerts, err := c.trackPeriods(ctx, buildTrackPeriodsRequest(employeeID, day, periods, false))
	var pv *PolicyViolationError
	if errors.As(err, &pv) && pv.onlyWarnings() {
		c.logger.Info("personio: confirming policy warnings with autoFix", "alerts", len(pv.Alerts))
		return c.trackPeriods(ctx, buildTrackPeriodsRequest(employeeID, day, periods, true))
	}
	return alerts, err
}

func (c *UIClient) trackPeriods(ctx context.Context, req trackPeriodsRequest) ([]Alert, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("encode track periods: %w", err)
	}
	resp, err := c.do(ctx, http.MethodPost, "/graphql?op="+trackPeriodsOperation, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, statusErr("track periods", resp)
	}
	var parsed trackPeriodsResponse
	if err := decodeLimited(resp, &parsed); err != nil {
		return nil, fmt.Errorf("decode track periods: %w", err)
	}
	if len(parsed.Errors) > 0 {
		return nil, graphQLErr(parsed.Errors)
	}
	if parsed.Data == nil || parsed.Data.TrackAttendancePeriodsOnDay == nil || parsed.Data.TrackAttendancePeriodsOnDay.Saved == nil {
		return nil, errors.New("personio: track periods: Antwort ohne gespeicherte Perioden")
	}
	saved := parsed.Data.TrackAttendancePeriodsOnDay.Saved.AlertsList
	alerts := make([]Alert, 0, len(saved))
	for _, m := range saved {
		alerts = append(alerts, parseSavedAlert(m))
	}
	return alerts, nil
}

func buildTrackPeriodsRequest(employeeID int64, day time.Time, periods []Period, autoFix bool) trackPeriodsRequest {
	person := gqlID{ID: strconv.FormatInt(employeeID, 10)}
	list := make([]gqlAttendancePeriod, 0, len(periods))
	for _, p := range periods {
		gp := gqlAttendancePeriod{
			ID:             gqlID{ID: p.ID},
			Type:           attendancePeriodWork,
			StartLocalTime: toLocalTime(p.Start),
			EndLocalTime:   toLocalTime(p.End),
			Comment:        p.Comment,
			PersonID:       person,
			Origin:         attendanceOriginWeb,
			Duration:       &gqlDuration{Seconds: strconv.FormatInt(int64(p.End.Sub(p.Start)/time.Second), 10)},
		}
		if p.ProjectID != nil {
			// Assumption: projectId uses the same {"id":"…"} wrapper as every
			// other id in this API. The captured tenant has no trackable
			// projects, so the shape is not confirmed by a HAR sample.
			gp.ProjectID = &gqlID{ID: strconv.FormatInt(*p.ProjectID, 10)}
		}
		list = append(list, gp)
	}
	var req trackPeriodsRequest
	req.Query = trackPeriodsQueryHash
	req.OperationName = trackPeriodsOperation
	req.Variables.Input = trackPeriodsInput{
		AutoFix:                autoFix,
		Origin:                 attendanceOriginWeb,
		AssumedAttributionDate: toDate(day),
		PersonID:               person,
		AttendancePeriods:      list,
	}
	return req
}

func toDate(t time.Time) gqlDate {
	l := t.Local()
	return gqlDate{Year: l.Year(), Month: int(l.Month()), Day: l.Day()}
}

func toLocalTime(t time.Time) gqlLocalTime {
	l := t.Local()
	return gqlLocalTime{
		Date: toDate(l),
		Time: gqlTimeOfDay{Hours: l.Hour(), Minutes: l.Minute(), Seconds: l.Second()},
	}
}

func graphQLErr(errs []gqlError) error {
	msgs := make([]string, 0, len(errs))
	for _, e := range errs {
		code := strings.ToUpper(e.Extensions.Code)
		if strings.Contains(code, "PERSISTED_QUERY_NOT_FOUND") || strings.Contains(strings.ToLower(e.Message), "persistedquerynotfound") {
			return ErrOperationOutdated
		}
		if pv := policyViolation(e); pv != nil {
			return pv
		}
		msgs = append(msgs, e.String())
	}
	return fmt.Errorf("personio: track periods: %s", strings.Join(msgs, "; "))
}

// policyViolation extracts validation alerts from a FAILED_PRECONDITION
// error. Shape (HAR 2026-10-17):
// details[].validation.original.alerts[] = {"enforcement":…,"date":…,"<kind>":{…}}.
func policyViolation(e gqlError) *PolicyViolationError {
	if e.Message != "FAILED_PRECONDITION" {
		return nil
	}
	var details []struct {
		Validation struct {
			Original struct {
				Alerts []map[string]json.RawMessage `json:"alerts"`
			} `json:"original"`
		} `json:"validation"`
	}
	if err := json.Unmarshal(e.Extensions.Details, &details); err != nil {
		return nil
	}
	pv := &PolicyViolationError{Title: e.Extensions.Title}
	for _, d := range details {
		for _, m := range d.Validation.Original.Alerts {
			pv.Alerts = append(pv.Alerts, parseErrorAlert(m))
		}
	}
	if len(pv.Alerts) == 0 {
		return nil
	}
	if pv.Title == "" {
		pv.Title = "Regelverstoß"
	}
	return pv
}

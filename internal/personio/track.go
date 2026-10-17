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
	if d := strings.TrimSpace(string(e.Extensions.Details)); d != "" && d != "null" && d != "[]" && d != "{}" {
		b.WriteString(" " + truncate(d, 300))
	}
	return truncate(b.String(), 500)
}

type trackPeriodsResponse struct {
	Data *struct {
		TrackAttendancePeriodsOnDay *struct {
			Saved *struct {
				AttendancePeriodsList []json.RawMessage `json:"attendancePeriodsList"`
			} `json:"saved"`
		} `json:"trackAttendancePeriodsOnDay"`
	} `json:"data"`
	Errors []gqlError `json:"errors"`
}

// TrackPeriodsOnDay replaces all attendance periods of the local calendar
// day `day` with `periods`, using the same GraphQL mutation as the Personio
// web app. The day is addressed by date — Personio no longer needs a day_id
// for writes. autoFix=false mirrors the web client.
func (c *UIClient) TrackPeriodsOnDay(ctx context.Context, employeeID int64, day time.Time, periods []Period) error {
	if employeeID == 0 {
		return errors.New("personio: employee id is zero")
	}
	body, err := json.Marshal(buildTrackPeriodsRequest(employeeID, day, periods))
	if err != nil {
		return fmt.Errorf("encode track periods: %w", err)
	}
	resp, err := c.do(ctx, http.MethodPost, "/graphql?op="+trackPeriodsOperation, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return statusErr("track periods", resp)
	}
	var parsed trackPeriodsResponse
	if err := decodeLimited(resp, &parsed); err != nil {
		return fmt.Errorf("decode track periods: %w", err)
	}
	if len(parsed.Errors) > 0 {
		return graphQLErr(parsed.Errors)
	}
	if parsed.Data == nil || parsed.Data.TrackAttendancePeriodsOnDay == nil || parsed.Data.TrackAttendancePeriodsOnDay.Saved == nil {
		return errors.New("personio: track periods: Antwort ohne gespeicherte Perioden")
	}
	return nil
}

func buildTrackPeriodsRequest(employeeID int64, day time.Time, periods []Period) trackPeriodsRequest {
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
		AutoFix:                false,
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
		msgs = append(msgs, e.String())
	}
	return fmt.Errorf("personio: track periods: %s", strings.Join(msgs, "; "))
}

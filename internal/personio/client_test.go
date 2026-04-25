package personio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type stubStore struct{ secret string }

func (s stubStore) GetSecret() (string, error)   { return s.secret, nil }
func (s stubStore) SetSecret(string) error       { return nil }
func (s stubStore) DeleteSecret() error          { return nil }

func TestClient_AuthThenCreateAttendance(t *testing.T) {
	t.Parallel()

	var authCalls, attendanceCalls int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth":
			authCalls++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"token":      "TKN",
					"expires_in": 3600,
				},
			})
		case "/company/attendances":
			attendanceCalls++
			if got := r.Header.Get("Authorization"); got != "Bearer TKN" {
				t.Errorf("missing/incorrect auth header: %q", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "ATT-1"}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := New(Options{
		BaseURL:    srv.URL,
		ClientID:   "id",
		EmployeeID: "42",
		Store:      stubStore{secret: "shh"},
	})

	res, err := c.CreateAttendance(context.Background(), AttendanceCreate{
		EmployeeID: "42",
		Date:       "2026-04-25",
		StartTime:  "09:00",
		EndTime:    "10:00",
		ProjectID:  "PRJ",
		Comment:    "#projekta",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if res.ID != "ATT-1" {
		t.Errorf("unexpected id: %v", res)
	}

	// Second call must reuse the cached token.
	if _, err := c.CreateAttendance(context.Background(), AttendanceCreate{
		EmployeeID: "42",
		Date:       "2026-04-25",
		StartTime:  "10:00",
		EndTime:    "11:00",
	}); err != nil {
		t.Fatalf("second create: %v", err)
	}
	if authCalls != 1 {
		t.Errorf("expected token to be cached, auth called %d times", authCalls)
	}
	if attendanceCalls != 2 {
		t.Errorf("attendance calls=%d", attendanceCalls)
	}
}

package personio

import (
	"testing"
	"time"
)

func TestSession_Expired(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	for _, tc := range []struct {
		name       string
		capturedAt time.Time
		want       bool
	}{
		{"zero captured_at counts as expired", time.Time{}, true},
		{"freshly captured", now, false},
		{"just under the limit", now.Add(-MaxSessionAge + time.Minute), false},
		{"exactly at the limit", now.Add(-MaxSessionAge), true},
		{"comfortably aged out", now.Add(-MaxSessionAge - time.Hour), true},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := Session{CapturedAt: tc.capturedAt}
			if got := s.Expired(); got != tc.want {
				t.Fatalf("Expired()=%v want %v (captured_at=%v)", got, tc.want, tc.capturedAt)
			}
		})
	}
}

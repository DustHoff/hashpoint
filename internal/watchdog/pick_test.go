package watchdog

import (
	"testing"
	"time"
)

func TestPickLastAlive(t *testing.T) {
	now := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	observed := time.Date(2026, 5, 29, 11, 59, 50, 0, time.UTC)
	marker := time.Date(2026, 5, 29, 11, 59, 30, 0, time.UTC) // coarser 30s heartbeat
	future := now.Add(time.Hour)

	tests := []struct {
		name             string
		observed, marker time.Time
		want             time.Time
	}{
		{"observation beats marker", observed, marker, observed},
		{"marker used when no observation", time.Time{}, marker, marker},
		{"both zero falls back to now", time.Time{}, time.Time{}, now},
		{"future-dated marker clamped to now", time.Time{}, future, now},
		{"future-dated observation clamped to now", future, marker, now},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pickLastAlive(tt.observed, tt.marker, now)
			if !got.Equal(tt.want) {
				t.Fatalf("pickLastAlive(%v, %v, %v) = %v, want %v", tt.observed, tt.marker, now, got, tt.want)
			}
			if got.Location() != time.UTC {
				t.Fatalf("result not UTC: %v", got.Location())
			}
		})
	}
}

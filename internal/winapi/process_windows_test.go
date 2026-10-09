//go:build windows

package winapi

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCurrentProcessCreationTime(t *testing.T) {
	got, err := CurrentProcessCreationTime()
	if err != nil {
		t.Fatalf("CurrentProcessCreationTime: %v", err)
	}
	if got.IsZero() || got.After(time.Now()) {
		t.Fatalf("creation time = %v, want non-zero and in the past", got)
	}
	if got.Location() != time.UTC {
		t.Errorf("creation time location = %v, want UTC", got.Location())
	}
}

func TestProcessLiveness_Self(t *testing.T) {
	created, err := CurrentProcessCreationTime()
	if err != nil {
		t.Fatalf("CurrentProcessCreationTime: %v", err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	image := filepath.Base(exe)

	tests := []struct {
		name       string
		wantCreate time.Time
		want       bool
	}{
		{"exact creation time", created, true},
		// The regression: a marker armed seconds after process creation (slow
		// startup) must not exceed the tolerance once it records the real
		// creation time; an arm-time stamp beyond the tolerance reads as reuse.
		{"arm time beyond tolerance", created.Add(creationTimeTolerance + time.Second), false},
		{"zero skips guard", time.Time{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			alive, err := ProcessLiveness(os.Getpid(), image, tt.wantCreate)
			if err != nil {
				t.Fatalf("ProcessLiveness: %v", err)
			}
			if alive != tt.want {
				t.Errorf("alive = %v, want %v", alive, tt.want)
			}
		})
	}
}

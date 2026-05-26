package feedback

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dusthoff/hashpoint/internal/crashguard"
)

func TestReadRecentCrashes_SelectsSanitisesAndOrders(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "timetracker.log")
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	mustWrite(t, path, strings.Join([]string{
		// Normal info line — no event field, ignored.
		`{"time":"2026-05-16T11:50:00Z","level":"INFO","msg":"hello"}`,
		// Crash but out of window (11h ago, window=1h) — dropped.
		`{"time":"2026-05-16T01:00:00Z","level":"ERROR","msg":"old","event":"panic","cause":"x"}`,
		// Panic with stack + a sensitive field that must be stripped.
		`{"time":"2026-05-16T11:30:00Z","level":"ERROR","msg":"recovered panic","event":"panic","goroutine":"tracker-tick","cause":"boom","stack":"goroutine 1 [running]","window_title":"Secret"}`,
		// Unclean shutdown, newer than the panic.
		`{"time":"2026-05-16T11:55:00Z","level":"ERROR","msg":"previous run did not exit cleanly","event":"unclean_shutdown","prev_pid":99,"downtime_sec":42}`,
		// Non-JSON line — dropped.
		`garbage`,
	}, "\n")+"\n")

	got, err := ReadRecentCrashes(context.Background(), path, LogWindowHour, now)
	if err != nil {
		t.Fatalf("ReadRecentCrashes: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d crashes, want 2: %+v", len(got), got)
	}
	// Newest first: unclean_shutdown (11:55) before panic (11:30).
	if got[0].Event != crashguard.EventUncleanShutdown {
		t.Errorf("first event=%q want unclean_shutdown", got[0].Event)
	}
	if got[1].Event != crashguard.EventPanic {
		t.Errorf("second event=%q want panic", got[1].Event)
	}
	if got[1].Cause != "boom" || got[1].Stack == "" {
		t.Errorf("panic cause/stack not captured: %+v", got[1])
	}
	if _, leaked := got[1].Detail["window_title"]; leaked {
		t.Error("window_title leaked into Detail")
	}
	if _, dup := got[1].Detail["cause"]; dup {
		t.Error("cause duplicated into Detail")
	}
	if got[1].Detail["goroutine"] != "tracker-tick" {
		t.Errorf("goroutine detail missing: %+v", got[1].Detail)
	}
	if got[0].Detail["prev_pid"] == nil {
		t.Errorf("prev_pid detail missing: %+v", got[0].Detail)
	}
}

func TestReadRecentCrashes_CapsAtMax(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "timetracker.log")
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	var b strings.Builder
	for i := 0; i < MaxCrashRecords+5; i++ {
		b.WriteString(`{"time":"2026-05-16T11:55:00Z","level":"ERROR","msg":"recovered panic","event":"panic","cause":"x"}`)
		b.WriteByte('\n')
	}
	mustWrite(t, path, b.String())

	got, err := ReadRecentCrashes(context.Background(), path, LogWindowHour, now)
	if err != nil {
		t.Fatalf("ReadRecentCrashes: %v", err)
	}
	if len(got) != MaxCrashRecords {
		t.Errorf("got %d crashes, want cap of %d", len(got), MaxCrashRecords)
	}
}

func TestReadRecentCrashes_MissingFile(t *testing.T) {
	got, err := ReadRecentCrashes(context.Background(), filepath.Join(t.TempDir(), "nope.log"), LogWindowToday, time.Now())
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if got != nil {
		t.Fatalf("missing file should return nil, got %d", len(got))
	}
}

func TestReadRecentCrashes_UnknownWindow(t *testing.T) {
	_, err := ReadRecentCrashes(context.Background(), filepath.Join(t.TempDir(), "x.log"), LogWindow("forever"), time.Now())
	if err == nil {
		t.Fatal("expected error for unknown window")
	}
}

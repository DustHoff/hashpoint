package feedback

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadLogTail_FiltersAndSanitises(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "timetracker.log")
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	mustWrite(t, path, strings.Join([]string{
		// Out of window when window=1h (10h ago)
		`{"time":"2026-05-16T02:00:00Z","level":"INFO","msg":"early"}`,
		// Debug — must be dropped
		`{"time":"2026-05-16T11:30:00Z","level":"DEBUG","msg":"poll","window_title":"Secret"}`,
		// Info with a sensitive field — keep the record but strip the field
		`{"time":"2026-05-16T11:45:00Z","level":"INFO","msg":"closed","window_title":"Slack — Chat","duration_sec":120}`,
		// Warn inside window — kept verbatim
		`{"time":"2026-05-16T11:55:00Z","level":"WARN","msg":"retry"}`,
		// Garbage non-JSON line — dropped silently
		`raw stderr noise`,
	}, "\n")+"\n")

	out, err := ReadLogTail(context.Background(), path, LogWindowHour, now)
	if err != nil {
		t.Fatalf("ReadLogTail: %v", err)
	}
	s := string(out)
	if strings.Contains(s, "early") {
		t.Errorf("out-of-window record present: %s", s)
	}
	if strings.Contains(s, `"DEBUG"`) || strings.Contains(s, "poll") {
		t.Errorf("debug record not dropped: %s", s)
	}
	if strings.Contains(s, "window_title") || strings.Contains(s, "Secret") || strings.Contains(s, "Slack") {
		t.Errorf("sensitive field leaked: %s", s)
	}
	if !strings.Contains(s, `"msg":"closed"`) || !strings.Contains(s, `"duration_sec":120`) {
		t.Errorf("info record dropped or stripped beyond sensitive fields: %s", s)
	}
	if !strings.Contains(s, `"msg":"retry"`) {
		t.Errorf("warn record dropped: %s", s)
	}
	if strings.Contains(s, "raw stderr noise") {
		t.Errorf("non-JSON line included: %s", s)
	}
}

func TestReadLogTail_TodayUsesLocalMidnight(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "timetracker.log")
	// "today" boundary depends on the local zone; pick now=10:00 local
	// and an entry one hour after local midnight.
	now := time.Date(2026, 5, 16, 10, 0, 0, 0, time.Local)
	midnightUTC := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local).UTC()
	included := midnightUTC.Add(1 * time.Hour).Format(time.RFC3339)
	excluded := midnightUTC.Add(-2 * time.Hour).Format(time.RFC3339)
	mustWrite(t, path, strings.Join([]string{
		`{"time":"` + excluded + `","level":"INFO","msg":"yesterday"}`,
		`{"time":"` + included + `","level":"INFO","msg":"today"}`,
	}, "\n")+"\n")

	out, err := ReadLogTail(context.Background(), path, LogWindowToday, now)
	if err != nil {
		t.Fatalf("ReadLogTail: %v", err)
	}
	s := string(out)
	if strings.Contains(s, "yesterday") {
		t.Errorf("yesterday record included: %s", s)
	}
	if !strings.Contains(s, "today") {
		t.Errorf("today record dropped: %s", s)
	}
}

func TestReadLogTail_TailsToMaxBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "timetracker.log")
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	// Write ~150 KB of in-window entries; result must be <=
	// MaxLogTailBytes and must start on a newline boundary (so it's
	// always a whole record).
	var b strings.Builder
	for i := 0; i < 3000; i++ {
		b.WriteString(`{"time":"2026-05-16T11:55:00Z","level":"INFO","msg":"`)
		b.WriteString(strings.Repeat("x", 40))
		b.WriteString(`","seq":`)
		b.WriteString("12345")
		b.WriteString("}\n")
	}
	mustWrite(t, path, b.String())
	out, err := ReadLogTail(context.Background(), path, LogWindowHour, now)
	if err != nil {
		t.Fatalf("ReadLogTail: %v", err)
	}
	if len(out) > MaxLogTailBytes {
		t.Fatalf("tail exceeds max: got %d, max %d", len(out), MaxLogTailBytes)
	}
	if len(out) == 0 || !strings.HasPrefix(string(out), `{`) {
		t.Fatalf("tail does not start on JSON boundary: %q", string(out)[:min(80, len(out))])
	}
}

func TestReadLogTail_MissingFile(t *testing.T) {
	out, err := ReadLogTail(context.Background(), filepath.Join(t.TempDir(), "nope.log"), LogWindowToday, time.Now())
	if err != nil {
		t.Fatalf("missing file should not error, got: %v", err)
	}
	if out != nil {
		t.Fatalf("missing file should return nil, got %d bytes", len(out))
	}
}

func TestReadLogTail_UnknownWindow(t *testing.T) {
	_, err := ReadLogTail(context.Background(), filepath.Join(t.TempDir(), "x.log"), LogWindow("forever"), time.Now())
	if err == nil {
		t.Fatal("expected error for unknown window")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

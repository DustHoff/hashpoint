package feedback

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"
)

// LogWindow names the user-facing log-range choices in the Feedback
// form. The mapping to a concrete cutoff happens inside ReadLogTail.
type LogWindow string

// Log-window choices surfaced in the UI dropdown.
const (
	LogWindowToday LogWindow = "today" // since local midnight
	LogWindowHour  LogWindow = "1h"    // last 60 minutes
	LogWindowDay   LogWindow = "24h"   // last 24 hours
)

// sensitiveLogFields lists keys whose values are stripped from log
// records before they reach the issue body. CLAUDE.md §5 forbids
// logging window titles at Info+; the orchestrator and tracker
// nonetheless attach the field on Debug, and Debug lines themselves
// are dropped — but a sloppy plugin could promote one to Warn, so
// strip defensively at the field level too.
var sensitiveLogFields = map[string]struct{}{
	"window_title": {},
	"title":        {},
}

// ReadLogTail loads the active log file, drops Debug-level records,
// removes sensitive fields, filters by window, and returns the last
// MaxLogTailBytes of the result.
//
// The returned slice ends on a newline boundary so the issue-body
// embedding stays readable. Non-JSON lines are dropped (the logger is
// configured for JSON output in production); an empty file or one
// whose entries are all outside the window returns nil with no error.
func ReadLogTail(ctx context.Context, logPath string, window LogWindow, now time.Time) ([]byte, error) {
	cutoff, ok := cutoffFor(window, now)
	if !ok {
		return nil, fmt.Errorf("feedback: unknown log window %q", window)
	}
	f, err := os.Open(logPath) // #nosec G304 -- logPath is the app's own log file, supplied via Paths.LogDir.
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("open log: %w", err)
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	// slog JSON records can be long when fields are nested; raise the
	// buffer ceiling so a single oversized line doesn't truncate the
	// whole tail.
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var buf []byte
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		out, ok := sanitizeLine(line, cutoff)
		if !ok {
			continue
		}
		buf = append(buf, out...)
		buf = append(buf, '\n')
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("scan log: %w", err)
	}
	return tailBytes(buf, MaxLogTailBytes), nil
}

// cutoffFor maps a LogWindow to a concrete cutoff time. The bool
// return distinguishes "unknown window" from "explicit empty cutoff".
func cutoffFor(w LogWindow, now time.Time) (time.Time, bool) {
	switch w {
	case LogWindowToday:
		local := now.Local()
		return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location()), true
	case LogWindowHour:
		return now.Add(-1 * time.Hour), true
	case LogWindowDay:
		return now.Add(-24 * time.Hour), true
	}
	return time.Time{}, false
}

// sanitizeLine parses one slog JSON record, drops it if Debug or
// out-of-window, scrubs sensitive fields, and re-marshals. Returns
// (line, false) when the record should be dropped entirely.
func sanitizeLine(line []byte, cutoff time.Time) ([]byte, bool) {
	var record map[string]any
	if err := json.Unmarshal(line, &record); err != nil {
		// Non-JSON lines (e.g. stderr leakage) are dropped — keeping
		// them risks emitting unstructured content with unknown
		// provenance into a public issue.
		return nil, false
	}
	if lvl, _ := record["level"].(string); strings.EqualFold(lvl, slog.LevelDebug.String()) {
		return nil, false
	}
	if ts, ok := recordTime(record); ok && ts.Before(cutoff) {
		return nil, false
	}
	for k := range sensitiveLogFields {
		delete(record, k)
	}
	out, err := json.Marshal(record)
	if err != nil {
		return nil, false
	}
	return out, true
}

// recordTime extracts a slog time field. slog's JSON handler emits
// "time" as an RFC3339(Nano) string; older slog versions or custom
// handlers might use a numeric Unix timestamp — both are accepted.
func recordTime(record map[string]any) (time.Time, bool) {
	switch v := record["time"].(type) {
	case string:
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			return t, true
		}
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t, true
		}
	case float64:
		return time.Unix(int64(v), 0), true
	}
	return time.Time{}, false
}

// tailBytes returns the last n bytes of buf, aligned to the next
// newline so the result starts on a complete log line.
func tailBytes(buf []byte, n int) []byte {
	if len(buf) <= n {
		return buf
	}
	tail := buf[len(buf)-n:]
	if i := bytes.IndexByte(tail, '\n'); i >= 0 && i+1 < len(tail) {
		return tail[i+1:]
	}
	return tail
}

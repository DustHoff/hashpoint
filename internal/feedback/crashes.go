package feedback

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"github.com/dusthoff/hashpoint/internal/crashguard"
)

// CrashRecord is one detected abnormal-termination event extracted from the
// JSON application log for embedding in a feedback issue. Stack holds the full
// recovered-panic stack when present; unclean-shutdown and UI-crash records
// carry no Go stack and leave it empty.
type CrashRecord struct {
	Time   time.Time
	Event  string // one of the crashguard.Event* values
	Msg    string
	Cause  string         // panic cause / error string, when present
	Stack  string         // full stack, when present
	Detail map[string]any // remaining structured fields (prev_pid, downtime_sec, exit_code, …)
}

// MaxCrashRecords caps how many crash entries are embedded in an issue so a
// crash loop cannot blow up the body size.
const MaxCrashRecords = 10

// crashEvents is the set of event-field values ReadRecentCrashes collects.
var crashEvents = map[string]struct{}{
	crashguard.EventPanic:           {},
	crashguard.EventUncleanShutdown: {},
	crashguard.EventUICrash:         {},
}

// crashOwnFields are lifted into dedicated CrashRecord fields and so omitted
// from Detail to avoid duplication.
var crashOwnFields = map[string]struct{}{
	"time": {}, "level": {}, "msg": {}, "event": {},
	"cause": {}, "stack": {}, "err": {},
}

// ReadRecentCrashes scans the JSON log at logPath for crash records at or after
// the window's cutoff and returns up to MaxCrashRecords of them, newest first.
// It mirrors ReadLogTail's tolerance: a missing file yields nil, non-JSON and
// out-of-window lines are skipped, and sensitive fields are stripped. Stacks are
// kept verbatim — Go tracebacks carry function names and hex argument words, not
// string contents, so they do not leak window titles.
func ReadRecentCrashes(ctx context.Context, logPath string, window LogWindow, now time.Time) ([]CrashRecord, error) {
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
	// Crash records (with a full stack) can be large; raise the ceiling so a
	// single record is never truncated mid-scan.
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var out []CrashRecord
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rec, ok := parseCrashLine(scanner.Bytes(), cutoff)
		if !ok {
			continue
		}
		out = append(out, rec)
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("scan log: %w", err)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	if len(out) > MaxCrashRecords {
		out = out[:MaxCrashRecords]
	}
	return out, nil
}

// parseCrashLine parses one slog JSON record and returns a CrashRecord when the
// line is an in-window crash event. Returns (_, false) for non-JSON lines,
// non-crash records, and records older than cutoff.
func parseCrashLine(line []byte, cutoff time.Time) (CrashRecord, bool) {
	if len(line) == 0 {
		return CrashRecord{}, false
	}
	var record map[string]any
	if err := json.Unmarshal(line, &record); err != nil {
		return CrashRecord{}, false
	}
	ev, _ := record["event"].(string)
	if _, ok := crashEvents[ev]; !ok {
		return CrashRecord{}, false
	}
	ts, ok := recordTime(record)
	if ok && ts.Before(cutoff) {
		return CrashRecord{}, false
	}
	rec := CrashRecord{Time: ts, Event: ev, Detail: map[string]any{}}
	rec.Msg, _ = record["msg"].(string)
	if rec.Cause, _ = record["cause"].(string); rec.Cause == "" {
		rec.Cause, _ = record["err"].(string)
	}
	rec.Stack, _ = record["stack"].(string)
	// Detail carries only allowlisted, non-own keys: an allowlist keeps a
	// PII-bearing field (tenant, employee_id, path, …) from riding into the
	// public crash report just because it shared a log line with a crash.
	for k, v := range record {
		if _, own := crashOwnFields[k]; own {
			continue
		}
		if _, ok := allowedLogFields[k]; !ok {
			continue
		}
		rec.Detail[k] = v
	}
	return rec, true
}

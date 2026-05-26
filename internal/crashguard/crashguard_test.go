package crashguard

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

// bufLogger returns a JSON logger writing into the returned buffer, so tests can
// assert on the structured crash records. Shared across the package's tests.
func bufLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewJSONHandler(&buf, nil)), &buf
}

func TestSafe_RecoversAndLogsPanic(t *testing.T) {
	logger, buf := bufLogger()
	Safe(logger, "unit", func() { panic("boom") })
	// Reaching here proves the panic was swallowed.

	var rec map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &rec); err != nil {
		t.Fatalf("log line is not JSON: %v (%q)", err, buf.String())
	}
	if rec["event"] != EventPanic {
		t.Errorf("event=%v want %q", rec["event"], EventPanic)
	}
	if rec["goroutine"] != "unit" {
		t.Errorf("goroutine=%v want unit", rec["goroutine"])
	}
	if cause, _ := rec["cause"].(string); !strings.Contains(cause, "boom") {
		t.Errorf("cause=%q does not contain panic value", cause)
	}
	if stack, _ := rec["stack"].(string); !strings.Contains(stack, "crashguard") {
		t.Errorf("stack missing crashguard frames: %q", stack)
	}
}

func TestSafe_CleanRunDoesNotLog(t *testing.T) {
	logger, buf := bufLogger()
	ran := false
	Safe(logger, "unit", func() { ran = true })
	if !ran {
		t.Fatal("fn did not run")
	}
	if buf.Len() != 0 {
		t.Errorf("clean run logged something: %q", buf.String())
	}
}

func TestRecover_AsDeferredHandler(t *testing.T) {
	logger, buf := bufLogger()
	func() {
		defer Recover(logger, "deferred")
		panic("kaboom")
	}()
	out := buf.String()
	if !strings.Contains(out, "kaboom") || !strings.Contains(out, EventPanic) {
		t.Errorf("Recover did not log the panic: %q", out)
	}
}

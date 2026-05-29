package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
)

// TestBoundHostAPILogCapsLevelAndNamespaces verifies that a plugin's Log call
// is downgraded to at most Info and that its field keys are namespaced under
// "plugin." Both protect the feedback log-tail bundle (uploaded to a public
// issue): the level cap stops a plugin injecting Warn/Error records, and the
// namespacing stops a plugin value from landing under a sanitizer-allowlisted
// host key such as "cause".
func TestBoundHostAPILogCapsLevelAndNamespaces(t *testing.T) {
	var buf bytes.Buffer
	api := &boundHostAPI{
		pluginName: "evil",
		log:        slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	if err := api.Log(context.Background(), "error", "boom", map[string]string{"cause": "secret", "plugin": "spoof"}); err != nil {
		t.Fatalf("Log: %v", err)
	}
	rec := decodeLogRecord(t, &buf)
	if lvl, _ := rec["level"].(string); lvl != "INFO" {
		t.Errorf("plugin error must be capped to INFO, got level=%q", lvl)
	}
	if _, raw := rec["cause"]; raw {
		t.Errorf("plugin field must be namespaced, found raw 'cause': %v", rec)
	}
	if rec["plugin.cause"] != "secret" {
		t.Errorf("expected namespaced plugin.cause=secret, got %v", rec["plugin.cause"])
	}
	if rec["plugin"] == "spoof" {
		t.Errorf("plugin must not override the host-injected plugin attr: %v", rec["plugin"])
	}
}

// TestBoundHostAPILogKeepsDebug verifies Debug-level plugin logs stay Debug
// (they are dropped from feedback bundles entirely).
func TestBoundHostAPILogKeepsDebug(t *testing.T) {
	var buf bytes.Buffer
	api := &boundHostAPI{
		pluginName: "p",
		log:        slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	if err := api.Log(context.Background(), "debug", "trace", nil); err != nil {
		t.Fatalf("Log: %v", err)
	}
	rec := decodeLogRecord(t, &buf)
	if lvl, _ := rec["level"].(string); lvl != "DEBUG" {
		t.Errorf("debug must stay DEBUG, got %q", lvl)
	}
}

func decodeLogRecord(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var rec map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &rec); err != nil {
		t.Fatalf("decode log record %q: %v", buf.String(), err)
	}
	return rec
}

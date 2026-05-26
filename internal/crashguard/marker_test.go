package crashguard

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStart_NoPreviousMarker_NoReport(t *testing.T) {
	dir := t.TempDir()
	logger, buf := bufLogger()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mk, err := Start(ctx, dir, RoleMonolith, Info{Version: "v1"}, logger)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer mk.Disarm()

	if strings.Contains(buf.String(), EventUncleanShutdown) {
		t.Errorf("clean first start reported unclean shutdown: %q", buf.String())
	}
	if _, err := os.Stat(filepath.Join(dir, markerDirName, "monolith.alive")); err != nil {
		t.Errorf("marker not written: %v", err)
	}
}

func TestStart_StaleMarker_ReportsUnclean(t *testing.T) {
	dir := t.TempDir()
	writeMarker(t, dir, "monolith.alive", markerFile{
		PID: 4242, Mode: "monolith", Version: "v0",
		StartUTC:  time.Now().UTC().Add(-2 * time.Hour),
		LastAlive: time.Now().UTC().Add(-90 * time.Minute),
	})
	logger, buf := bufLogger()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mk, err := Start(ctx, dir, RoleMonolith, Info{Version: "v1"}, logger)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer mk.Disarm()

	out := buf.String()
	if !strings.Contains(out, EventUncleanShutdown) {
		t.Fatalf("stale marker not reported: %q", out)
	}
	if !strings.Contains(out, "4242") {
		t.Errorf("prev_pid missing from report: %q", out)
	}
	if !strings.Contains(out, "downtime_sec") {
		t.Errorf("downtime missing from report: %q", out)
	}
}

func TestStart_CorruptMarker_ReportsUnclean(t *testing.T) {
	dir := t.TempDir()
	mdir := filepath.Join(dir, markerDirName)
	if err := os.MkdirAll(mdir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mdir, "collector.alive"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	logger, buf := bufLogger()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mk, err := Start(ctx, dir, RoleCollector, Info{}, logger)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer mk.Disarm()

	if !strings.Contains(buf.String(), EventUncleanShutdown) {
		t.Errorf("corrupt marker not reported as unclean: %q", buf.String())
	}
}

func TestDisarm_RemovesMarkerAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	logger, _ := bufLogger()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mk, err := Start(ctx, dir, RoleUI, Info{}, logger)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	path := filepath.Join(dir, markerDirName, "ui.alive")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("marker missing before disarm: %v", err)
	}

	mk.Disarm()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("marker still present after disarm: %v", err)
	}
	mk.Disarm() // second call: no-op, must not panic

	var nilMk *Marker
	nilMk.Disarm() // nil receiver: no-op, must not panic
}

func TestHeartbeat_AdvancesLastAlive(t *testing.T) {
	dir := t.TempDir()
	logger, _ := bufLogger()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mk, err := Start(ctx, dir, RoleMonolith, Info{}, logger)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer mk.Disarm()
	path := filepath.Join(dir, markerDirName, "monolith.alive")

	first := readLastAlive(t, path)
	time.Sleep(5 * time.Millisecond)
	mk.Heartbeat()
	second := readLastAlive(t, path)

	if !second.After(first) {
		t.Errorf("heartbeat did not advance last_alive: first=%v second=%v", first, second)
	}
}

func TestHeartbeat_NoOpAfterDisarm(t *testing.T) {
	dir := t.TempDir()
	logger, _ := bufLogger()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mk, err := Start(ctx, dir, RoleMonolith, Info{}, logger)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	path := filepath.Join(dir, markerDirName, "monolith.alive")

	mk.Disarm()
	mk.Heartbeat() // must not resurrect the removed marker
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("heartbeat resurrected marker after disarm: %v", err)
	}
}

func writeMarker(t *testing.T, dir, name string, mf markerFile) {
	t.Helper()
	mdir := filepath.Join(dir, markerDirName)
	if err := os.MkdirAll(mdir, 0o700); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(mf)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mdir, name), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readLastAlive(t *testing.T, path string) time.Time {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	var mf markerFile
	if err := json.Unmarshal(b, &mf); err != nil {
		t.Fatalf("unmarshal marker: %v", err)
	}
	return mf.LastAlive
}

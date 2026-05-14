package plugin

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func newDiscardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// writeFile is a t.Helper that writes content to path, creating parent dirs.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	return string(b)
}

func TestSeedIfMissing_NoSeedDir(t *testing.T) {
	root := t.TempDir()
	pluginsDir := filepath.Join(root, "plugins")
	seedDir := filepath.Join(root, "seed-that-does-not-exist")

	if err := SeedIfMissing(seedDir, pluginsDir, newDiscardLogger()); err != nil {
		t.Fatalf("SeedIfMissing: %v", err)
	}
	// pluginsDir intentionally NOT created when there is nothing to seed —
	// the host's discovery loop tolerates a missing PluginsDir already.
	if _, err := os.Stat(pluginsDir); !os.IsNotExist(err) {
		t.Errorf("pluginsDir should not exist when seedDir is missing, stat err=%v", err)
	}
}

func TestSeedIfMissing_EmptySeedDir(t *testing.T) {
	root := t.TempDir()
	seedDir := filepath.Join(root, "seed")
	pluginsDir := filepath.Join(root, "plugins")
	if err := os.MkdirAll(seedDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := SeedIfMissing(seedDir, pluginsDir, newDiscardLogger()); err != nil {
		t.Fatalf("SeedIfMissing: %v", err)
	}
	entries, err := os.ReadDir(pluginsDir)
	if err != nil {
		t.Fatalf("read pluginsDir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected pluginsDir empty, got %d entries", len(entries))
	}
}

func TestSeedIfMissing_CopiesIntoEmptyTarget(t *testing.T) {
	root := t.TempDir()
	seedDir := filepath.Join(root, "seed")
	pluginsDir := filepath.Join(root, "plugins")

	writeFile(t, filepath.Join(seedDir, "plugin-manager", "plugin-manager.exe"), "EXE-BYTES")
	writeFile(t, filepath.Join(seedDir, "plugin-manager", "manifest.toml"), "name=\"plugin-manager\"\n")

	if err := SeedIfMissing(seedDir, pluginsDir, newDiscardLogger()); err != nil {
		t.Fatalf("SeedIfMissing: %v", err)
	}
	if got := readFile(t, filepath.Join(pluginsDir, "plugin-manager", "plugin-manager.exe")); got != "EXE-BYTES" {
		t.Errorf("copied exe content mismatch: %q", got)
	}
	if got := readFile(t, filepath.Join(pluginsDir, "plugin-manager", "manifest.toml")); got != "name=\"plugin-manager\"\n" {
		t.Errorf("copied manifest content mismatch: %q", got)
	}
}

func TestSeedIfMissing_PreservesExistingTarget(t *testing.T) {
	root := t.TempDir()
	seedDir := filepath.Join(root, "seed")
	pluginsDir := filepath.Join(root, "plugins")

	writeFile(t, filepath.Join(seedDir, "plugin-manager", "plugin-manager.exe"), "SEED-VERSION")
	writeFile(t, filepath.Join(seedDir, "plugin-manager", "manifest.toml"), "version=\"1.0.0\"\n")
	// User has already updated this plugin via the in-app PM UI.
	writeFile(t, filepath.Join(pluginsDir, "plugin-manager", "plugin-manager.exe"), "USER-VERSION")
	writeFile(t, filepath.Join(pluginsDir, "plugin-manager", "manifest.toml"), "version=\"2.5.0\"\n")

	if err := SeedIfMissing(seedDir, pluginsDir, newDiscardLogger()); err != nil {
		t.Fatalf("SeedIfMissing: %v", err)
	}
	if got := readFile(t, filepath.Join(pluginsDir, "plugin-manager", "plugin-manager.exe")); got != "USER-VERSION" {
		t.Errorf("user version was overwritten: %q", got)
	}
	if got := readFile(t, filepath.Join(pluginsDir, "plugin-manager", "manifest.toml")); got != "version=\"2.5.0\"\n" {
		t.Errorf("user manifest was overwritten: %q", got)
	}
}

func TestSeedIfMissing_SeedsOnlyMissingPlugins(t *testing.T) {
	root := t.TempDir()
	seedDir := filepath.Join(root, "seed")
	pluginsDir := filepath.Join(root, "plugins")

	writeFile(t, filepath.Join(seedDir, "alpha", "alpha.exe"), "ALPHA-SEED")
	writeFile(t, filepath.Join(seedDir, "alpha", "manifest.toml"), "name=\"alpha\"\n")
	writeFile(t, filepath.Join(seedDir, "beta", "beta.exe"), "BETA-SEED")
	writeFile(t, filepath.Join(seedDir, "beta", "manifest.toml"), "name=\"beta\"\n")

	// alpha already installed by the user; beta is fresh.
	writeFile(t, filepath.Join(pluginsDir, "alpha", "alpha.exe"), "ALPHA-USER")
	writeFile(t, filepath.Join(pluginsDir, "alpha", "manifest.toml"), "name=\"alpha\"\n")

	if err := SeedIfMissing(seedDir, pluginsDir, newDiscardLogger()); err != nil {
		t.Fatalf("SeedIfMissing: %v", err)
	}
	if got := readFile(t, filepath.Join(pluginsDir, "alpha", "alpha.exe")); got != "ALPHA-USER" {
		t.Errorf("alpha was overwritten: %q", got)
	}
	if got := readFile(t, filepath.Join(pluginsDir, "beta", "beta.exe")); got != "BETA-SEED" {
		t.Errorf("beta was not seeded: %q", got)
	}
}

func TestSeedIfMissing_RecursiveSubdirectories(t *testing.T) {
	root := t.TempDir()
	seedDir := filepath.Join(root, "seed")
	pluginsDir := filepath.Join(root, "plugins")

	writeFile(t, filepath.Join(seedDir, "complex", "complex.exe"), "EXE")
	writeFile(t, filepath.Join(seedDir, "complex", "manifest.toml"), "name=\"complex\"\n")
	writeFile(t, filepath.Join(seedDir, "complex", "assets", "icon.ico"), "ICO")
	writeFile(t, filepath.Join(seedDir, "complex", "templates", "nested", "tpl.txt"), "TPL")

	if err := SeedIfMissing(seedDir, pluginsDir, newDiscardLogger()); err != nil {
		t.Fatalf("SeedIfMissing: %v", err)
	}
	if got := readFile(t, filepath.Join(pluginsDir, "complex", "assets", "icon.ico")); got != "ICO" {
		t.Errorf("nested asset not copied: %q", got)
	}
	if got := readFile(t, filepath.Join(pluginsDir, "complex", "templates", "nested", "tpl.txt")); got != "TPL" {
		t.Errorf("deeply nested file not copied: %q", got)
	}
}

func TestSeedIfMissing_IgnoresNonDirectoryEntries(t *testing.T) {
	root := t.TempDir()
	seedDir := filepath.Join(root, "seed")
	pluginsDir := filepath.Join(root, "plugins")

	// Stray file in seedDir — should be ignored, not treated as plugin.
	writeFile(t, filepath.Join(seedDir, "stray.txt"), "stray")
	writeFile(t, filepath.Join(seedDir, "real", "real.exe"), "REAL")

	if err := SeedIfMissing(seedDir, pluginsDir, newDiscardLogger()); err != nil {
		t.Fatalf("SeedIfMissing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(pluginsDir, "stray.txt")); !os.IsNotExist(err) {
		t.Errorf("stray file leaked into pluginsDir, stat err=%v", err)
	}
	if got := readFile(t, filepath.Join(pluginsDir, "real", "real.exe")); got != "REAL" {
		t.Errorf("real plugin not seeded: %q", got)
	}
}

func TestSeedIfMissing_AtomicityLeavesNoTmpOnSuccess(t *testing.T) {
	root := t.TempDir()
	seedDir := filepath.Join(root, "seed")
	pluginsDir := filepath.Join(root, "plugins")

	writeFile(t, filepath.Join(seedDir, "alpha", "alpha.exe"), "ALPHA")

	if err := SeedIfMissing(seedDir, pluginsDir, newDiscardLogger()); err != nil {
		t.Fatalf("SeedIfMissing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(pluginsDir, "alpha.tmp")); !os.IsNotExist(err) {
		t.Errorf("stale .tmp left behind after successful seed, stat err=%v", err)
	}
}

func TestSeedIfMissing_ClearsStaleTmp(t *testing.T) {
	root := t.TempDir()
	seedDir := filepath.Join(root, "seed")
	pluginsDir := filepath.Join(root, "plugins")

	writeFile(t, filepath.Join(seedDir, "alpha", "alpha.exe"), "ALPHA-NEW")
	// Simulate a previous interrupted seed: leftover *.tmp must not block
	// the next attempt (the function clears it before re-copying).
	writeFile(t, filepath.Join(pluginsDir, "alpha.tmp", "garbage"), "left-over")

	if err := SeedIfMissing(seedDir, pluginsDir, newDiscardLogger()); err != nil {
		t.Fatalf("SeedIfMissing: %v", err)
	}
	if got := readFile(t, filepath.Join(pluginsDir, "alpha", "alpha.exe")); got != "ALPHA-NEW" {
		t.Errorf("alpha not seeded over stale tmp: %q", got)
	}
	if _, err := os.Stat(filepath.Join(pluginsDir, "alpha.tmp")); !os.IsNotExist(err) {
		t.Errorf("stale .tmp not cleared, stat err=%v", err)
	}
}

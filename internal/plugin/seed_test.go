package plugin

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func newDiscardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

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

// writePluginBundle writes a minimal-but-valid plugin layout (an exe and
// manifest.toml whose name matches the directory) into baseDir/name.
func writePluginBundle(t *testing.T, baseDir, name, version, exeBody string) {
	t.Helper()
	pluginDir := filepath.Join(baseDir, name)
	writeFile(t, filepath.Join(pluginDir, name+".exe"), exeBody)
	manifest := fmt.Sprintf(`name        = %q
version     = %q
api_version = 1
description = "test plugin"
`, name, version)
	writeFile(t, filepath.Join(pluginDir, "manifest.toml"), manifest)
}

func TestSeed_NoSeedDir(t *testing.T) {
	root := t.TempDir()
	pluginsDir := filepath.Join(root, "plugins")
	seedDir := filepath.Join(root, "seed-that-does-not-exist")

	if err := Seed(seedDir, pluginsDir, newDiscardLogger()); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if _, err := os.Stat(pluginsDir); !os.IsNotExist(err) {
		t.Errorf("pluginsDir should not exist when seedDir is missing, stat err=%v", err)
	}
}

func TestSeed_EmptySeedDir(t *testing.T) {
	root := t.TempDir()
	seedDir := filepath.Join(root, "seed")
	pluginsDir := filepath.Join(root, "plugins")
	if err := os.MkdirAll(seedDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := Seed(seedDir, pluginsDir, newDiscardLogger()); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	entries, err := os.ReadDir(pluginsDir)
	if err != nil {
		t.Fatalf("read pluginsDir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected pluginsDir empty, got %d entries", len(entries))
	}
}

func TestSeed_CopiesIntoEmptyTarget(t *testing.T) {
	root := t.TempDir()
	seedDir := filepath.Join(root, "seed")
	pluginsDir := filepath.Join(root, "plugins")

	writePluginBundle(t, seedDir, "plugin-manager", "1.0.0", "EXE-BYTES")

	if err := Seed(seedDir, pluginsDir, newDiscardLogger()); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if got := readFile(t, filepath.Join(pluginsDir, "plugin-manager", "plugin-manager.exe")); got != "EXE-BYTES" {
		t.Errorf("copied exe content mismatch: %q", got)
	}
	if _, err := os.Stat(filepath.Join(pluginsDir, "plugin-manager", "manifest.toml")); err != nil {
		t.Errorf("manifest not copied: %v", err)
	}
}

func TestSeed_PreservesNewerInstalled(t *testing.T) {
	root := t.TempDir()
	seedDir := filepath.Join(root, "seed")
	pluginsDir := filepath.Join(root, "plugins")

	writePluginBundle(t, seedDir, "plugin-manager", "1.0.0", "SEED-VERSION")
	// User updated to a newer release via the in-app Plugins UI.
	writePluginBundle(t, pluginsDir, "plugin-manager", "2.5.0", "USER-VERSION")

	if err := Seed(seedDir, pluginsDir, newDiscardLogger()); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if got := readFile(t, filepath.Join(pluginsDir, "plugin-manager", "plugin-manager.exe")); got != "USER-VERSION" {
		t.Errorf("newer user version was overwritten: %q", got)
	}
}

func TestSeed_PreservesEqualVersion(t *testing.T) {
	root := t.TempDir()
	seedDir := filepath.Join(root, "seed")
	pluginsDir := filepath.Join(root, "plugins")

	writePluginBundle(t, seedDir, "plugin-manager", "1.2.3", "SEED")
	writePluginBundle(t, pluginsDir, "plugin-manager", "1.2.3", "INSTALLED")

	if err := Seed(seedDir, pluginsDir, newDiscardLogger()); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if got := readFile(t, filepath.Join(pluginsDir, "plugin-manager", "plugin-manager.exe")); got != "INSTALLED" {
		t.Errorf("equal version triggered overwrite: %q", got)
	}
}

func TestSeed_UpgradesOlderInstalled(t *testing.T) {
	root := t.TempDir()
	seedDir := filepath.Join(root, "seed")
	pluginsDir := filepath.Join(root, "plugins")

	writePluginBundle(t, seedDir, "plugin-manager", "2.0.0", "SEED-NEWER")
	writePluginBundle(t, pluginsDir, "plugin-manager", "1.0.0", "INSTALLED-OLDER")

	if err := Seed(seedDir, pluginsDir, newDiscardLogger()); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if got := readFile(t, filepath.Join(pluginsDir, "plugin-manager", "plugin-manager.exe")); got != "SEED-NEWER" {
		t.Errorf("older install was not upgraded: %q", got)
	}
}

func TestSeed_UpgradeReplacesStaleFiles(t *testing.T) {
	root := t.TempDir()
	seedDir := filepath.Join(root, "seed")
	pluginsDir := filepath.Join(root, "plugins")

	writePluginBundle(t, seedDir, "alpha", "2.0.0", "ALPHA-NEW")
	writePluginBundle(t, pluginsDir, "alpha", "1.0.0", "ALPHA-OLD")
	// A stale file from the older bundle that the new bundle no longer has.
	writeFile(t, filepath.Join(pluginsDir, "alpha", "obsolete.dat"), "stale")

	if err := Seed(seedDir, pluginsDir, newDiscardLogger()); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if got := readFile(t, filepath.Join(pluginsDir, "alpha", "alpha.exe")); got != "ALPHA-NEW" {
		t.Errorf("alpha.exe not upgraded: %q", got)
	}
	if _, err := os.Stat(filepath.Join(pluginsDir, "alpha", "obsolete.dat")); !os.IsNotExist(err) {
		t.Errorf("stale file from prior install survived upgrade, stat err=%v", err)
	}
}

func TestSeed_SkipsWhenInstalledManifestUnreadable(t *testing.T) {
	root := t.TempDir()
	seedDir := filepath.Join(root, "seed")
	pluginsDir := filepath.Join(root, "plugins")

	writePluginBundle(t, seedDir, "plugin-manager", "2.0.0", "SEED")
	// Target dir exists, but the manifest is missing — we cannot tell what
	// is installed, so play it safe and keep the user's bytes.
	writeFile(t, filepath.Join(pluginsDir, "plugin-manager", "plugin-manager.exe"), "USER-MAYBE-MODIFIED")

	if err := Seed(seedDir, pluginsDir, newDiscardLogger()); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if got := readFile(t, filepath.Join(pluginsDir, "plugin-manager", "plugin-manager.exe")); got != "USER-MAYBE-MODIFIED" {
		t.Errorf("seed overwrote user bytes despite unreadable installed manifest: %q", got)
	}
}

func TestSeed_SkipsWhenSeedManifestUnreadable(t *testing.T) {
	root := t.TempDir()
	seedDir := filepath.Join(root, "seed")
	pluginsDir := filepath.Join(root, "plugins")

	// Bundle without a manifest (or with garbage). The seed code cannot
	// determine the bundled version, so it must not touch the existing
	// install.
	writeFile(t, filepath.Join(seedDir, "plugin-manager", "plugin-manager.exe"), "SEED-BUT-INVALID")
	writePluginBundle(t, pluginsDir, "plugin-manager", "1.0.0", "INSTALLED")

	if err := Seed(seedDir, pluginsDir, newDiscardLogger()); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if got := readFile(t, filepath.Join(pluginsDir, "plugin-manager", "plugin-manager.exe")); got != "INSTALLED" {
		t.Errorf("seed overwrote install despite unreadable seed manifest: %q", got)
	}
}

func TestSeed_MixedFreshAndKeptAndUpgraded(t *testing.T) {
	root := t.TempDir()
	seedDir := filepath.Join(root, "seed")
	pluginsDir := filepath.Join(root, "plugins")

	writePluginBundle(t, seedDir, "alpha", "1.0.0", "ALPHA-SEED")  // user has newer
	writePluginBundle(t, seedDir, "beta", "2.0.0", "BETA-SEED")    // user has older
	writePluginBundle(t, seedDir, "gamma", "1.0.0", "GAMMA-FRESH") // user has none

	writePluginBundle(t, pluginsDir, "alpha", "1.5.0", "ALPHA-USER")
	writePluginBundle(t, pluginsDir, "beta", "1.0.0", "BETA-USER")

	if err := Seed(seedDir, pluginsDir, newDiscardLogger()); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if got := readFile(t, filepath.Join(pluginsDir, "alpha", "alpha.exe")); got != "ALPHA-USER" {
		t.Errorf("alpha: newer user version overwritten: %q", got)
	}
	if got := readFile(t, filepath.Join(pluginsDir, "beta", "beta.exe")); got != "BETA-SEED" {
		t.Errorf("beta: older user version not upgraded: %q", got)
	}
	if got := readFile(t, filepath.Join(pluginsDir, "gamma", "gamma.exe")); got != "GAMMA-FRESH" {
		t.Errorf("gamma: fresh install not seeded: %q", got)
	}
}

func TestSeed_RecursiveSubdirectories(t *testing.T) {
	root := t.TempDir()
	seedDir := filepath.Join(root, "seed")
	pluginsDir := filepath.Join(root, "plugins")

	writePluginBundle(t, seedDir, "complex", "1.0.0", "EXE")
	writeFile(t, filepath.Join(seedDir, "complex", "assets", "icon.ico"), "ICO")
	writeFile(t, filepath.Join(seedDir, "complex", "templates", "nested", "tpl.txt"), "TPL")

	if err := Seed(seedDir, pluginsDir, newDiscardLogger()); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if got := readFile(t, filepath.Join(pluginsDir, "complex", "assets", "icon.ico")); got != "ICO" {
		t.Errorf("nested asset not copied: %q", got)
	}
	if got := readFile(t, filepath.Join(pluginsDir, "complex", "templates", "nested", "tpl.txt")); got != "TPL" {
		t.Errorf("deeply nested file not copied: %q", got)
	}
}

func TestSeed_IgnoresNonDirectoryEntries(t *testing.T) {
	root := t.TempDir()
	seedDir := filepath.Join(root, "seed")
	pluginsDir := filepath.Join(root, "plugins")

	writeFile(t, filepath.Join(seedDir, "stray.txt"), "stray")
	writePluginBundle(t, seedDir, "real", "1.0.0", "REAL")

	if err := Seed(seedDir, pluginsDir, newDiscardLogger()); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(pluginsDir, "stray.txt")); !os.IsNotExist(err) {
		t.Errorf("stray file leaked into pluginsDir, stat err=%v", err)
	}
	if got := readFile(t, filepath.Join(pluginsDir, "real", "real.exe")); got != "REAL" {
		t.Errorf("real plugin not seeded: %q", got)
	}
}

func TestSeed_AtomicityLeavesNoTmpOnSuccess(t *testing.T) {
	root := t.TempDir()
	seedDir := filepath.Join(root, "seed")
	pluginsDir := filepath.Join(root, "plugins")

	writePluginBundle(t, seedDir, "alpha", "1.0.0", "ALPHA")

	if err := Seed(seedDir, pluginsDir, newDiscardLogger()); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(pluginsDir, "alpha.tmp")); !os.IsNotExist(err) {
		t.Errorf("stale .tmp left behind after successful seed, stat err=%v", err)
	}
}

func TestSeed_ClearsStaleTmpOnFreshInstall(t *testing.T) {
	root := t.TempDir()
	seedDir := filepath.Join(root, "seed")
	pluginsDir := filepath.Join(root, "plugins")

	writePluginBundle(t, seedDir, "alpha", "1.0.0", "ALPHA-NEW")
	// Simulate a previous interrupted seed: a leftover *.tmp dir must not
	// block the next attempt.
	writeFile(t, filepath.Join(pluginsDir, "alpha.tmp", "garbage"), "left-over")

	if err := Seed(seedDir, pluginsDir, newDiscardLogger()); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if got := readFile(t, filepath.Join(pluginsDir, "alpha", "alpha.exe")); got != "ALPHA-NEW" {
		t.Errorf("alpha not seeded over stale tmp: %q", got)
	}
	if _, err := os.Stat(filepath.Join(pluginsDir, "alpha.tmp")); !os.IsNotExist(err) {
		t.Errorf("stale .tmp not cleared, stat err=%v", err)
	}
}

func TestVersionIsNewer(t *testing.T) {
	cases := []struct {
		name      string
		seed      string
		installed string
		want      bool
	}{
		{"strictly newer patch", "1.0.1", "1.0.0", true},
		{"strictly newer minor", "1.1.0", "1.0.99", true},
		{"strictly newer major", "2.0.0", "1.99.99", true},
		{"older than installed", "1.0.0", "1.0.1", false},
		{"equal versions", "1.0.0", "1.0.0", false},
		{"missing trailing parts treated as zero", "1.0", "1.0.0", false},
		{"missing trailing parts treated as zero, newer", "1.1", "1.0.99", true},
		{"v-prefix on seed", "v2.0.0", "1.0.0", true},
		{"v-prefix on installed", "2.0.0", "v1.0.0", true},
		{"two-digit minor beats one-digit", "1.10.0", "1.9.0", true},
		{"pre-release loses to release", "1.0.0-beta", "1.0.0", false},
		{"release beats pre-release", "1.0.0", "1.0.0-beta", true},
		{"pre-release lex order", "1.0.0-beta", "1.0.0-alpha", true},
		{"installed unparseable", "1.0.0", "not-a-version", false},
		{"seed unparseable", "not-a-version", "1.0.0", false},
		{"both empty", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := versionIsNewer(tc.seed, tc.installed)
			if got != tc.want {
				t.Errorf("versionIsNewer(%q, %q) = %v, want %v",
					tc.seed, tc.installed, got, tc.want)
			}
		})
	}
}

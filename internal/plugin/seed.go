package plugin

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
)

// SeedIfMissing copies each subdirectory of seedDir into pluginsDir, but
// only when the target subdirectory does not already exist. Existing
// targets are left strictly untouched.
//
// Motivation: a per-machine MSI installer cannot reliably write to the
// interactive user's %APPDATA% — it runs as admin (or SYSTEM under SCCM)
// where %APPDATA% resolves to the wrong profile. The installer therefore
// drops plugin bundles next to hashpoint.exe under plugins-seed\<name>\,
// and the app — which always runs as the interactive user — seeds them
// into the per-user PluginsDir on launch.
//
// Re-seeding policy: a plugin is seeded only when its target directory
// does not exist. That preserves user-side updates installed via the
// plugin-management UI across MSI reinstalls (the bundled version becomes
// the floor, never the cap).
//
// Behaviour:
//   - seedDir does not exist (e.g. developer build without an MSI): no-op,
//     nil error.
//   - pluginsDir does not exist: created with 0o700.
//   - For each subdirectory in seedDir: if the target exists, skip;
//     otherwise copy the tree into <pluginsDir>\<name>.tmp and rename it
//     atomically to <pluginsDir>\<name>.
//   - Per-plugin copy failures are logged at Warn and processing continues
//     with the next plugin. The function returns a non-nil error only when
//     it cannot read seedDir at all or cannot ensure pluginsDir.
//
// Non-directory entries in seedDir are ignored — bundles must always be
// directories that match the plugin's manifest name.
func SeedIfMissing(seedDir, pluginsDir string, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	entries, err := os.ReadDir(seedDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			logger.Debug("plugin seed: directory missing — skipping", "seed_dir", seedDir)
			return nil
		}
		return fmt.Errorf("read seed dir %q: %w", seedDir, err)
	}
	if err := os.MkdirAll(pluginsDir, 0o700); err != nil {
		return fmt.Errorf("ensure plugins dir %q: %w", pluginsDir, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		src := filepath.Join(seedDir, name)
		dst := filepath.Join(pluginsDir, name)
		if _, err := os.Stat(dst); err == nil {
			logger.Debug("plugin seed: target exists — skipping", "plugin", name)
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			logger.Warn("plugin seed: stat target failed",
				"plugin", name, "err", err)
			continue
		}
		if err := seedOne(src, dst); err != nil {
			logger.Warn("plugin seed: copy failed",
				"plugin", name, "err", err)
			continue
		}
		logger.Info("plugin seed: copied", "plugin", name)
	}
	return nil
}

// seedOne copies src to a sibling temp directory of dst and renames it
// into place, so a partial copy never becomes visible as a real plugin
// directory to the discovery loop.
func seedOne(src, dst string) error {
	tmp := dst + ".tmp"
	if err := os.RemoveAll(tmp); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("clear stale tmp %q: %w", tmp, err)
	}
	if err := copyTree(src, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.RemoveAll(tmp)
		return fmt.Errorf("rename %q to %q: %w", tmp, dst, err)
	}
	return nil
}

func copyTree(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stat %q: %w", src, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%q: not a directory", src)
	}
	if err := os.MkdirAll(dst, info.Mode().Perm()); err != nil {
		return fmt.Errorf("mkdir %q: %w", dst, err)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return fmt.Errorf("read %q: %w", src, err)
	}
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := copyTree(s, d); err != nil {
				return err
			}
			continue
		}
		if err := copyFile(s, d); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src) // #nosec G304 -- src derived from seedDir traversal.
	if err != nil {
		return fmt.Errorf("open %q: %w", src, err)
	}
	defer func() { _ = in.Close() }()
	info, err := in.Stat()
	if err != nil {
		return fmt.Errorf("stat %q: %w", src, err)
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return fmt.Errorf("create %q: %w", dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return fmt.Errorf("copy to %q: %w", dst, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close %q: %w", dst, err)
	}
	return nil
}

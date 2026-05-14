package plugin

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Seed copies each subdirectory of seedDir into pluginsDir when the target
// is missing or the bundled manifest's Version is strictly newer than the
// installed one. Identical or older bundles leave the user's installation
// strictly untouched.
//
// Motivation: a per-machine MSI installer cannot reliably write to the
// interactive user's %APPDATA% — it runs as admin (or SYSTEM under SCCM)
// where %APPDATA% resolves to the wrong profile. The installer therefore
// drops plugin bundles next to hashpoint.exe under plugins-seed\<name>\,
// and the app — which always runs as the interactive user — seeds them
// into the per-user PluginsDir on launch.
//
// Re-seeding policy:
//   - Target directory missing  → copy the bundle in.
//   - Target exists, bundle newer (semver) → overwrite atomically.
//   - Target exists, bundle older or equal → keep the user's install.
//   - Manifest unreadable on either side   → conservative skip.
//
// That way an MSI upgrade carrying a newer plugin-manager replaces the
// old one, but a user who installed an even newer version through the
// in-app Plugins UI is never silently rolled back.
//
// Behaviour:
//   - seedDir does not exist (e.g. developer build without an MSI): no-op,
//     nil error.
//   - pluginsDir does not exist: created with 0o700.
//   - Per-plugin failures are logged at Warn and processing continues
//     with the next plugin. The function returns a non-nil error only
//     when it cannot read seedDir at all or cannot ensure pluginsDir.
//
// Non-directory entries in seedDir are ignored — bundles must always be
// directories whose name matches the plugin's manifest name.
func Seed(seedDir, pluginsDir string, logger *slog.Logger) error {
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
		action, srcVer, dstVer := decideSeedAction(src, dst, logger, name)
		switch action {
		case seedActionSkip:
			continue
		case seedActionFresh:
			if err := seedOne(src, dst); err != nil {
				logger.Warn("plugin seed: copy failed",
					"plugin", name, "err", err)
				continue
			}
			logger.Info("plugin seed: copied", "plugin", name, "version", srcVer)
		case seedActionUpgrade:
			if err := seedOne(src, dst); err != nil {
				logger.Warn("plugin seed: upgrade failed",
					"plugin", name, "err", err)
				continue
			}
			logger.Info("plugin seed: upgraded",
				"plugin", name, "from_version", dstVer, "to_version", srcVer)
		}
	}
	return nil
}

type seedAction int

const (
	seedActionSkip seedAction = iota
	seedActionFresh
	seedActionUpgrade
)

// decideSeedAction inspects the source and target directories and returns
// the action to take plus the seed/installed version strings (best-effort,
// used for logging — may be empty on the skip path). The function never
// writes; the caller performs the actual copy.
func decideSeedAction(src, dst string, logger *slog.Logger, name string) (seedAction, string, string) {
	dstInfo, dstErr := os.Stat(dst)
	if dstErr != nil {
		if errors.Is(dstErr, fs.ErrNotExist) {
			srcVer := manifestVersion(src)
			return seedActionFresh, srcVer, ""
		}
		logger.Warn("plugin seed: stat target failed",
			"plugin", name, "err", dstErr)
		return seedActionSkip, "", ""
	}
	if !dstInfo.IsDir() {
		logger.Warn("plugin seed: target exists but is not a directory — skipping",
			"plugin", name)
		return seedActionSkip, "", ""
	}
	srcMf, srcErr := LoadManifest(src)
	if srcErr != nil {
		logger.Warn("plugin seed: cannot read bundled manifest — skipping",
			"plugin", name, "err", srcErr)
		return seedActionSkip, "", ""
	}
	dstMf, dstMfErr := LoadManifest(dst)
	if dstMfErr != nil {
		logger.Warn("plugin seed: cannot read installed manifest — leaving as-is",
			"plugin", name, "err", dstMfErr)
		return seedActionSkip, srcMf.Version, ""
	}
	if versionIsNewer(srcMf.Version, dstMf.Version) {
		return seedActionUpgrade, srcMf.Version, dstMf.Version
	}
	logger.Debug("plugin seed: installed version is at least as new — skipping",
		"plugin", name, "installed_version", dstMf.Version, "seed_version", srcMf.Version)
	return seedActionSkip, srcMf.Version, dstMf.Version
}

// manifestVersion returns the Version field of <dir>/manifest.toml or the
// empty string if the manifest cannot be loaded. Best-effort, used for
// logging the "freshly seeded" version.
func manifestVersion(dir string) string {
	m, err := LoadManifest(dir)
	if err != nil {
		return ""
	}
	return m.Version
}

// versionIsNewer reports whether seedVer is strictly newer than
// installedVer. Both arguments are parsed as semver-ish dotted-numeric
// strings with an optional 'v' prefix and an optional pre-release suffix
// (e.g. "1.2.3-beta"). Numeric base parts compare as integers; missing
// trailing parts default to 0 so "1.0" equals "1.0.0". A version WITHOUT
// a pre-release ranks above the same version WITH a pre-release.
//
// If either side is unparseable, the function returns false — the caller
// then takes the safe path and leaves the installed plugin alone.
func versionIsNewer(seedVer, installedVer string) bool {
	a, aOk := parseVersion(seedVer)
	b, bOk := parseVersion(installedVer)
	if !aOk || !bOk {
		return false
	}
	n := len(a.base)
	if len(b.base) > n {
		n = len(b.base)
	}
	for i := 0; i < n; i++ {
		ai, bi := 0, 0
		if i < len(a.base) {
			ai = a.base[i]
		}
		if i < len(b.base) {
			bi = b.base[i]
		}
		if ai != bi {
			return ai > bi
		}
	}
	switch {
	case a.pre == b.pre:
		return false
	case a.pre == "":
		return true
	case b.pre == "":
		return false
	default:
		return a.pre > b.pre
	}
}

type parsedVersion struct {
	base []int
	pre  string
}

func parseVersion(v string) (parsedVersion, bool) {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	if v == "" {
		return parsedVersion{}, false
	}
	var pre string
	if i := strings.Index(v, "-"); i >= 0 {
		pre = v[i+1:]
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	base := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return parsedVersion{}, false
		}
		base = append(base, n)
	}
	if len(base) == 0 {
		return parsedVersion{}, false
	}
	return parsedVersion{base: base, pre: pre}, true
}

// seedOne copies src into dst via a sibling .tmp directory. When dst
// already exists (upgrade path), the old contents are removed between
// the copy and the rename so the swap works on Windows too. A failure
// in the copy leaves the original dst intact; a failure after the
// remove leaves the target missing and the next launch re-seeds from
// scratch — self-healing.
func seedOne(src, dst string) error {
	tmp := dst + ".tmp"
	if err := os.RemoveAll(tmp); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("clear stale tmp %q: %w", tmp, err)
	}
	if err := copyTree(src, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	if err := os.RemoveAll(dst); err != nil && !errors.Is(err, fs.ErrNotExist) {
		_ = os.RemoveAll(tmp)
		return fmt.Errorf("remove old %q: %w", dst, err)
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
	// #nosec G304 -- dst is pluginsDir/<name>/<…> with name validated by
	// os.ReadDir of the host-resolved seedDir; not derived from user input.
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

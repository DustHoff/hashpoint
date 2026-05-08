package entra

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/cache"
)

// cipher abstracts the at-rest encryption layer so the rest of the cache
// machinery can be exercised in unit tests on any host (the production
// implementation is DPAPI on Windows; non-Windows builds fail loud).
type cipher interface {
	Encrypt(plaintext []byte) ([]byte, error)
	Decrypt(ciphertext []byte) ([]byte, error)
}

// fileCache implements cache.ExportReplace by persisting MSAL's serialised
// cache state into a single file, encrypted with a user-bound DPAPI key.
// The file is written atomically (temp + rename) so a crash mid-write
// can't leave a half-encrypted blob that would refuse to decrypt next
// run.
type fileCache struct {
	path   string
	cipher cipher
	logger *slog.Logger

	mu sync.Mutex
}

func newFileCache(path string, c cipher, logger *slog.Logger) *fileCache {
	if logger == nil {
		logger = slog.Default()
	}
	return &fileCache{path: path, cipher: c, logger: logger}
}

// Replace loads the on-disk ciphertext, decrypts it via DPAPI and feeds
// the plaintext to MSAL's in-memory cache. A missing file or a decryption
// failure is treated as "empty cache" — the user just has to log in
// again, which is far better than aborting startup with an error.
func (f *fileCache) Replace(ctx context.Context, mc cache.Unmarshaler, _ cache.ReplaceHints) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	blob, err := os.ReadFile(f.path) // #nosec G304 -- path is supplied by the manager from %LOCALAPPDATA%.
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read cache: %w", err)
	}
	if len(blob) == 0 {
		return nil
	}
	pt, err := f.cipher.Decrypt(blob)
	if err != nil {
		f.logger.Warn("entra: cache decrypt failed — treating as empty",
			"err", err)
		return nil
	}
	return mc.Unmarshal(pt)
}

// Export encrypts MSAL's serialised cache state and persists it to disk.
// An empty Marshal output (full cache cleared, e.g. after RemoveAccount
// of the last account) deletes the file so a stale ciphertext can't be
// resurrected by an old backup.
func (f *fileCache) Export(ctx context.Context, mc cache.Marshaler, _ cache.ExportHints) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	pt, err := mc.Marshal()
	if err != nil {
		return fmt.Errorf("marshal cache: %w", err)
	}
	if len(pt) == 0 {
		if err := os.Remove(f.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("clear cache: %w", err)
		}
		return nil
	}
	ct, err := f.cipher.Encrypt(pt)
	if err != nil {
		return fmt.Errorf("encrypt cache: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}
	return atomicWrite(f.path, ct, 0o600)
}

// Clear unconditionally deletes the cache file. Used by Logout so that
// even if MSAL's RemoveAccount path didn't trigger an Export, no leftover
// ciphertext remains under %LOCALAPPDATA%.
func (f *fileCache) Clear() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := os.Remove(f.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// atomicWrite writes data to a temp file in the destination directory,
// fsyncs it, then renames over the destination. The rename is atomic on
// the same filesystem; a crash either leaves the old file (if rename
// hadn't happened) or the new file — never a torn write.
func atomicWrite(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".msal_cache.*.tmp")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmp := f.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmp)
		}
	}()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("sync temp: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Chmod(tmp, perm); err != nil {
		return fmt.Errorf("chmod temp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename temp: %w", err)
	}
	cleanup = false
	return nil
}

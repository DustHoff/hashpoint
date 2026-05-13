package entra

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/cache"
)

// xorCipher is a stand-in for DPAPI in unit tests: it produces output
// that differs from input (so tests can assert "ciphertext is not
// plaintext") without touching the OS keychain.
type xorCipher struct{ key byte }

func (c xorCipher) transform(b []byte) []byte {
	out := make([]byte, len(b))
	for i, v := range b {
		out[i] = v ^ c.key
	}
	return out
}

func (c xorCipher) Encrypt(p []byte) ([]byte, error) {
	if len(p) == 0 {
		return nil, errors.New("empty")
	}
	return c.transform(p), nil
}

func (c xorCipher) Decrypt(b []byte) ([]byte, error) {
	if len(b) == 0 {
		return nil, errors.New("empty")
	}
	return c.transform(b), nil
}

// fakeMC stands in for MSAL's serialiser. Its Marshal/Unmarshal payload
// is the raw cache state.
type fakeMC struct {
	data []byte
}

func (f *fakeMC) Marshal() ([]byte, error)    { return f.data, nil }
func (f *fakeMC) Unmarshal(data []byte) error { f.data = append([]byte(nil), data...); return nil }

func TestFileCache_RoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fc := newFileCache(filepath.Join(dir, "msal.bin"), xorCipher{key: 0xA5}, nil)

	src := &fakeMC{data: []byte(`{"accounts":[{"id":"abc"}]}`)}
	if err := fc.Export(context.Background(), src, cache.ExportHints{}); err != nil {
		t.Fatalf("export: %v", err)
	}

	// Verify on-disk bytes are NOT plaintext.
	got, err := os.ReadFile(filepath.Join(dir, "msal.bin"))
	if err != nil {
		t.Fatalf("read disk: %v", err)
	}
	if bytes.Equal(got, src.data) {
		t.Fatal("on-disk blob equals plaintext — cipher bypassed")
	}

	dst := &fakeMC{}
	if err := fc.Replace(context.Background(), dst, cache.ReplaceHints{}); err != nil {
		t.Fatalf("replace: %v", err)
	}
	if !bytes.Equal(dst.data, src.data) {
		t.Fatalf("round-trip mismatch: got %q want %q", dst.data, src.data)
	}
}

func TestFileCache_ReplaceWithMissingFileIsEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fc := newFileCache(filepath.Join(dir, "msal.bin"), xorCipher{key: 1}, nil)
	dst := &fakeMC{data: []byte("stale")}
	if err := fc.Replace(context.Background(), dst, cache.ReplaceHints{}); err != nil {
		t.Fatalf("replace on missing file should succeed; got %v", err)
	}
	if dst.data == nil {
		t.Fatal("should not have written nil over stale value when file is missing")
	}
}

func TestFileCache_DecryptFailureTreatedAsEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "msal.bin")
	if err := os.WriteFile(path, []byte("garbage"), 0o600); err != nil {
		t.Fatalf("seed garbage: %v", err)
	}
	// Cipher rejects unaligned input length to simulate decrypt failure.
	bad := errCipher{decryptErr: errors.New("bad blob")}
	fc := newFileCache(path, bad, nil)
	dst := &fakeMC{}
	if err := fc.Replace(context.Background(), dst, cache.ReplaceHints{}); err != nil {
		t.Fatalf("replace should swallow decrypt errors; got %v", err)
	}
	if dst.data != nil {
		t.Fatalf("dst should remain empty when decrypt fails; got %q", dst.data)
	}
}

func TestFileCache_EmptyMarshalDeletesFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "msal.bin")
	fc := newFileCache(path, xorCipher{key: 0xA5}, nil)

	// Seed with content.
	if err := fc.Export(context.Background(), &fakeMC{data: []byte("payload")}, cache.ExportHints{}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file present after seed: %v", err)
	}
	// Now simulate "cache fully cleared".
	if err := fc.Export(context.Background(), &fakeMC{data: nil}, cache.ExportHints{}); err != nil {
		t.Fatalf("export empty: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected file removed; got err=%v", err)
	}
}

func TestFileCache_Clear(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "msal.bin")
	fc := newFileCache(path, xorCipher{key: 0xA5}, nil)
	if err := fc.Export(context.Background(), &fakeMC{data: []byte("payload")}, cache.ExportHints{}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := fc.Clear(); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected file removed after Clear; got err=%v", err)
	}
	// Clear on missing file is idempotent.
	if err := fc.Clear(); err != nil {
		t.Fatalf("Clear on missing file should be idempotent; got %v", err)
	}
}

type errCipher struct {
	encryptErr error
	decryptErr error
}

func (e errCipher) Encrypt(p []byte) ([]byte, error) {
	if e.encryptErr != nil {
		return nil, e.encryptErr
	}
	return p, nil
}
func (e errCipher) Decrypt(b []byte) ([]byte, error) {
	if e.decryptErr != nil {
		return nil, e.decryptErr
	}
	return b, nil
}

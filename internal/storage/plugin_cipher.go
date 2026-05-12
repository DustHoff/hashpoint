package storage

// Cipher encrypts and decrypts the byte blobs persisted in
// plugin_settings.value when a row is flagged is_secret=1. Production
// uses NewDPAPICipher (Windows DPAPI, CurrentUser scope); tests pass
// NoopCipher{}.
//
// Implementations must round-trip arbitrary bytes — callers pass UTF-8
// strings today but the repo treats values as opaque blobs.
type Cipher interface {
	Encrypt(plaintext []byte) ([]byte, error)
	Decrypt(ciphertext []byte) ([]byte, error)
}

// NoopCipher is a pass-through cipher for tests. It is intentionally
// not safe for production: secret values would land on disk unencrypted.
type NoopCipher struct{}

// Encrypt returns plaintext unchanged.
func (NoopCipher) Encrypt(p []byte) ([]byte, error) {
	out := make([]byte, len(p))
	copy(out, p)
	return out, nil
}

// Decrypt returns ciphertext unchanged.
func (NoopCipher) Decrypt(c []byte) ([]byte, error) {
	out := make([]byte, len(c))
	copy(out, c)
	return out, nil
}

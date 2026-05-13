//go:build !windows

package storage

import "errors"

// stubCipher exists so the package compiles on lint hosts. Any actual
// Encrypt/Decrypt call hard-fails — the production target is Windows
// and we don't want to silently degrade plugin secret handling on a CI
// build that accidentally exercises the production path.
type stubCipher struct{}

// NewDPAPICipher returns a stub on non-Windows builds.
func NewDPAPICipher() Cipher { return stubCipher{} }

// Encrypt always fails on non-Windows: DPAPI is a Windows API and we
// refuse to silently store plugin secrets in cleartext.
func (stubCipher) Encrypt(_ []byte) ([]byte, error) {
	return nil, errors.New("storage: DPAPI cipher is Windows-only")
}

// Decrypt always fails on non-Windows — see Encrypt.
func (stubCipher) Decrypt(_ []byte) ([]byte, error) {
	return nil, errors.New("storage: DPAPI cipher is Windows-only")
}

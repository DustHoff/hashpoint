//go:build !windows

package entra

import "errors"

// stubCipher exists so the package compiles on lint hosts. Any actual
// Encrypt/Decrypt call hard-fails — the production target is Windows and
// we don't want to silently degrade auth security for a CI build.
type stubCipher struct{}

func newDPAPICipher() cipher { return stubCipher{} }

func (stubCipher) Encrypt(_ []byte) ([]byte, error) {
	return nil, errors.New("entra: DPAPI cipher is Windows-only")
}

func (stubCipher) Decrypt(_ []byte) ([]byte, error) {
	return nil, errors.New("entra: DPAPI cipher is Windows-only")
}

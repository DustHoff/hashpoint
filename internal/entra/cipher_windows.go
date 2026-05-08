//go:build windows

package entra

import "github.com/onesi/hashpoint/internal/winapi"

// dpapiCipher uses Windows Data Protection API (CurrentUser scope) via
// winapi.ProtectDataCurrentUser / UnprotectDataCurrentUser. The
// resulting blob is bound to the Windows user account that wrote it —
// switching users (or copying the cache file to another machine) makes
// the data unreadable, which is exactly what we want.
type dpapiCipher struct{}

// newDPAPICipher returns the production cipher. Wired into the Manager
// from the Windows build only; non-Windows hosts use the stub from
// cipher_other.go which fails loud on Encrypt/Decrypt.
func newDPAPICipher() cipher { return dpapiCipher{} }

func (dpapiCipher) Encrypt(p []byte) ([]byte, error) {
	return winapi.ProtectDataCurrentUser(p)
}

func (dpapiCipher) Decrypt(c []byte) ([]byte, error) {
	return winapi.UnprotectDataCurrentUser(c)
}

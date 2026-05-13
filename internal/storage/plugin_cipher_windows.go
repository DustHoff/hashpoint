//go:build windows

package storage

import "github.com/dusthoff/hashpoint/internal/winapi"

// dpapiCipher binds plugin secret values to the current Windows user
// account via winapi.ProtectDataCurrentUser. The ciphertext can only be
// decrypted by the same user on the same machine — copying data.db to
// another box makes the blobs unreadable, which is the intended threat
// model (a stolen DB must not yield plaintext secrets).
type dpapiCipher struct{}

// NewDPAPICipher returns the production cipher. Wired into the
// PluginSettingsRepo from cmd/timetracker/main.go on Windows.
func NewDPAPICipher() Cipher { return dpapiCipher{} }

func (dpapiCipher) Encrypt(p []byte) ([]byte, error) {
	return winapi.ProtectDataCurrentUser(p)
}

func (dpapiCipher) Decrypt(c []byte) ([]byte, error) {
	return winapi.UnprotectDataCurrentUser(c)
}

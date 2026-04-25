//go:build windows

package personio

import (
	"errors"

	"github.com/danieljoos/wincred"
)

// WinCredStore stores the secret in the Windows Credential Manager.
type WinCredStore struct {
	Target string
}

// NewWinCredStore returns a wincred-backed store with the default target.
func NewWinCredStore() *WinCredStore { return &WinCredStore{Target: CredentialTarget} }

// GetSecret reads the secret from wincred.
func (s *WinCredStore) GetSecret() (string, error) {
	c, err := wincred.GetGenericCredential(s.target())
	if err != nil {
		// wincred returns "Element not found." — translate to ErrSecretNotSet.
		return "", errorsWrap(err, ErrSecretNotSet)
	}
	return string(c.CredentialBlob), nil
}

// SetSecret writes the secret to wincred.
func (s *WinCredStore) SetSecret(secret string) error {
	c := wincred.NewGenericCredential(s.target())
	c.CredentialBlob = []byte(secret)
	c.UserName = "TimeTracker"
	return c.Write()
}

// DeleteSecret removes the credential entry.
func (s *WinCredStore) DeleteSecret() error {
	c, err := wincred.GetGenericCredential(s.target())
	if err != nil {
		return nil
	}
	return c.Delete()
}

func (s *WinCredStore) target() string {
	if s.Target == "" {
		return CredentialTarget
	}
	return s.Target
}

func errorsWrap(err, sentinel error) error {
	if err == nil {
		return nil
	}
	return errors.Join(sentinel, err)
}

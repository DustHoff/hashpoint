//go:build windows

package feedback

import (
	"errors"

	"github.com/danieljoos/wincred"
)

// WinCredTokenStore stores the GitHub App tokens in the Windows
// Credential Manager. Mirrors the personio.WinCredSessionStore
// pattern; see CLAUDE.md §12.
type WinCredTokenStore struct {
	Target string
}

// NewWinCredTokenStore returns a wincred-backed token store using the
// package-level CredentialTarget.
func NewWinCredTokenStore() *WinCredTokenStore {
	return &WinCredTokenStore{Target: CredentialTarget}
}

// Get returns the stored token, or ErrNoToken if no entry exists.
func (s *WinCredTokenStore) Get() (*Token, error) {
	c, err := wincred.GetGenericCredential(s.target())
	if err != nil {
		return nil, errors.Join(ErrNoToken, err)
	}
	if len(c.CredentialBlob) == 0 {
		return nil, ErrNoToken
	}
	return UnmarshalToken(c.CredentialBlob)
}

// Set persists the token under CredentialTarget.
func (s *WinCredTokenStore) Set(t *Token) error {
	if t == nil {
		return errors.New("nil token")
	}
	blob, err := MarshalToken(t)
	if err != nil {
		return err
	}
	c := wincred.NewGenericCredential(s.target())
	c.CredentialBlob = blob
	c.UserName = "TimeTracker"
	return c.Write()
}

// Delete removes the credential entry (best-effort).
func (s *WinCredTokenStore) Delete() error {
	c, err := wincred.GetGenericCredential(s.target())
	if err != nil {
		return nil
	}
	return c.Delete()
}

func (s *WinCredTokenStore) target() string {
	if s.Target == "" {
		return CredentialTarget
	}
	return s.Target
}

//go:build windows

package personio

import (
	"errors"

	"github.com/danieljoos/wincred"
)

// WinCredSessionStore stores the captured session blob in the Windows
// Credential Manager.
type WinCredSessionStore struct {
	Target string
}

// NewWinCredSessionStore returns a wincred-backed session store.
func NewWinCredSessionStore() *WinCredSessionStore {
	return &WinCredSessionStore{Target: SessionCredentialTarget}
}

// Get reads and decodes the stored session, or returns ErrNoSession.
func (s *WinCredSessionStore) Get() (*Session, error) {
	c, err := wincred.GetGenericCredential(s.target())
	if err != nil {
		return nil, errors.Join(ErrNoSession, err)
	}
	if len(c.CredentialBlob) == 0 {
		return nil, ErrNoSession
	}
	return UnmarshalSession(c.CredentialBlob)
}

// Set persists the session to wincred.
func (s *WinCredSessionStore) Set(sess *Session) error {
	if sess == nil {
		return errors.New("nil session")
	}
	blob, err := MarshalSession(sess)
	if err != nil {
		return err
	}
	c := wincred.NewGenericCredential(s.target())
	c.CredentialBlob = blob
	c.UserName = "TimeTracker"
	return c.Write()
}

// Delete removes the credential entry, if any.
func (s *WinCredSessionStore) Delete() error {
	c, err := wincred.GetGenericCredential(s.target())
	if err != nil {
		return nil
	}
	return c.Delete()
}

func (s *WinCredSessionStore) target() string {
	if s.Target == "" {
		return SessionCredentialTarget
	}
	return s.Target
}

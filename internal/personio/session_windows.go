//go:build windows

package personio

import (
	"errors"
	"log/slog"

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
//
// Sessions older than MaxSessionAge are treated as if they did not exist
// and the underlying credential entry is purged on the way out — so callers
// (UI status, sync) consistently see "no session" and trigger a fresh
// interactive login instead of replaying stale cookies.
func (s *WinCredSessionStore) Get() (*Session, error) {
	c, err := wincred.GetGenericCredential(s.target())
	if err != nil {
		return nil, errors.Join(ErrNoSession, err)
	}
	if len(c.CredentialBlob) == 0 {
		return nil, ErrNoSession
	}
	sess, err := UnmarshalSession(c.CredentialBlob)
	if err != nil {
		return nil, err
	}
	if sess.Expired() {
		slog.Default().Info("personio: stored session exceeded max age — purging",
			"max_age", MaxSessionAge, "captured_at", sess.CapturedAt)
		_ = c.Delete()
		return nil, ErrNoSession
	}
	return sess, nil
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

//go:build !windows

package personio

// MemorySessionStore keeps the session in process memory. It exists so the
// non-Windows builds (CI, dev on macOS/Linux) compile and so unit tests have
// a drop-in replacement for the wincred-backed store on Windows.
type MemorySessionStore struct {
	s *Session
}

// NewMemorySessionStore returns an in-memory session store.
func NewMemorySessionStore() *MemorySessionStore { return &MemorySessionStore{} }

// Get returns the stored session or ErrNoSession. Sessions older than
// MaxSessionAge are dropped and reported as missing so the caller is
// pushed through a fresh interactive login.
func (m *MemorySessionStore) Get() (*Session, error) {
	if m.s == nil {
		return nil, ErrNoSession
	}
	if m.s.Expired() {
		m.s = nil
		return nil, ErrNoSession
	}
	return m.s, nil
}

// Set persists the session in memory.
func (m *MemorySessionStore) Set(s *Session) error {
	m.s = s
	return nil
}

// Delete clears the in-memory session.
func (m *MemorySessionStore) Delete() error {
	m.s = nil
	return nil
}

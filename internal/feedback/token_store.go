package feedback

import (
	"encoding/json"
	"errors"
	"time"
)

// ErrNoToken is returned by TokenStore.Get when no credential is stored
// or the stored blob has been purged.
var ErrNoToken = errors.New("feedback: no token stored")

// Token captures everything Device Flow returns plus the bookkeeping we
// need to drive the refresh path. Times are stored in UTC.
type Token struct {
	// AccessToken is the user-to-server token used as
	// "Authorization: Bearer ..." against api.github.com.
	AccessToken string `json:"access_token"`
	// RefreshToken is exchanged for a fresh access token after
	// ExpiresAt. Empty when "Expire user authorization tokens" is
	// disabled on the GitHub App — in that case AccessToken never
	// rotates.
	RefreshToken string `json:"refresh_token,omitempty"`
	// ExpiresAt is when AccessToken stops being accepted. Zero when
	// the App is configured for non-expiring user tokens.
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	// RefreshExpiresAt is when RefreshToken stops being accepted.
	// Zero when expiry is disabled. Past this point the user must
	// re-authorize from the UI.
	RefreshExpiresAt time.Time `json:"refresh_expires_at,omitempty"`
	// IssuedAt records when the token was minted (or last refreshed).
	// Surfaced in the UI for "verbunden seit ..." context.
	IssuedAt time.Time `json:"issued_at"`
	// Login is the GitHub login of the authorizing user, cached so the
	// connect-box can show "verbunden als <login>" without an extra
	// /user round-trip on every status read.
	Login string `json:"login,omitempty"`
}

// NeedsRefresh reports whether AccessToken should be exchanged for a
// fresh one before being used. The 30-second skew absorbs network
// latency so we don't ship a token that GitHub rejects mid-flight.
func (t Token) NeedsRefresh(now time.Time) bool {
	if t.ExpiresAt.IsZero() {
		return false
	}
	return !now.Before(t.ExpiresAt.Add(-30 * time.Second))
}

// RefreshExpired reports whether the refresh token itself is past its
// validity window. Callers must trigger a full re-authorization in
// that case.
func (t Token) RefreshExpired(now time.Time) bool {
	if t.RefreshExpiresAt.IsZero() {
		return false
	}
	return !now.Before(t.RefreshExpiresAt)
}

// TokenStore abstracts persistence for Device-Flow tokens. The Windows
// implementation in token_store_windows.go uses the Credential
// Manager; the non-Windows fallback in token_store_other.go keeps the
// token in process memory so CI / dev on macOS/Linux still compiles.
type TokenStore interface {
	Get() (*Token, error)
	Set(*Token) error
	Delete() error
}

// MarshalToken encodes the token for credential-manager storage.
func MarshalToken(t *Token) ([]byte, error) { return json.Marshal(t) }

// UnmarshalToken decodes a credential-manager blob back into a Token.
func UnmarshalToken(b []byte) (*Token, error) {
	var t Token
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// MemoryTokenStore is the cross-platform in-memory implementation.
// Used as the non-Windows production fallback (see
// default_store_other.go) and as the seam tests inject so they don't
// depend on the Credential Manager. Not safe for concurrent use —
// callers serialise via App.feedback.mu.
type MemoryTokenStore struct {
	t *Token
}

// NewMemoryTokenStore returns an empty in-memory token store.
func NewMemoryTokenStore() *MemoryTokenStore { return &MemoryTokenStore{} }

// Get returns the stored token or ErrNoToken.
func (m *MemoryTokenStore) Get() (*Token, error) {
	if m.t == nil {
		return nil, ErrNoToken
	}
	return m.t, nil
}

// Set persists the token in memory.
func (m *MemoryTokenStore) Set(t *Token) error {
	m.t = t
	return nil
}

// Delete clears the in-memory token.
func (m *MemoryTokenStore) Delete() error {
	m.t = nil
	return nil
}

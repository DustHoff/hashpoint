//go:build !windows

package feedback

// NewDefaultTokenStore returns an in-memory store on non-Windows
// builds. Mirrors the personio session-store split (CLAUDE.md §13:
// pure-Go, no CGO, so we avoid Linux/macOS keyring deps).
func NewDefaultTokenStore() TokenStore { return NewMemoryTokenStore() }

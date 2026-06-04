//go:build !windows

package winapi

// SessionLocked reports whether the interactive session is on a locked or
// secure desktop. Non-Windows builds (used only for Linux CI/linting) have no
// such concept for this app, so it always reports unlocked.
func SessionLocked() (bool, error) { return false, nil }

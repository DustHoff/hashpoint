//go:build !windows

package winapi

import "errors"

// ErrAlreadyRunning matches the Windows implementation's sentinel so callers
// can type-check on both platforms.
var ErrAlreadyRunning = errors.New("winapi: another instance already running")

// SingleInstanceLock is a no-op stub on non-Windows platforms. The Hashpoint
// CLI is Windows-only; this stub keeps the rest of the program compilable
// for linting and unit tests on Linux/macOS runners.
type SingleInstanceLock struct{}

// AcquireSingleInstanceLock is a no-op on non-Windows platforms — it
// always succeeds. Multi-instance protection is a Windows-specific concern
// rooted in the same WebView2/Wails lifecycle behaviour the rest of this
// package addresses.
func AcquireSingleInstanceLock(_ string) (*SingleInstanceLock, error) {
	return &SingleInstanceLock{}, nil
}

// Release is a no-op.
func (*SingleInstanceLock) Release() error { return nil }

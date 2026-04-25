// Package winapi wraps the Win32 calls needed for focus tracking.
//
// Every exported call is platform-portable (a non-Windows stub returns
// ErrUnsupported), so higher layers compile on Linux runners used for linting.
package winapi

import (
	"errors"
	"time"
)

// ErrUnsupported is returned by stub implementations on non-Windows platforms.
var ErrUnsupported = errors.New("winapi: unsupported on this platform")

// FocusInfo describes the currently focused foreground window.
type FocusInfo struct {
	HWND        uintptr
	PID         uint32
	ProcessName string
	ProcessPath string
	Title       string
}

// IsZero reports whether the FocusInfo represents an empty / unknown state
// (for example, when no foreground window can be detected, e.g. on the lock
// screen).
func (f FocusInfo) IsZero() bool {
	return f.HWND == 0 && f.PID == 0 && f.ProcessName == "" && f.Title == ""
}

// Foreground returns information about the currently focused window. It must
// be called from the same OS thread on every invocation; callers in Go can
// rely on the runtime to schedule consistently within one goroutine.
func Foreground() (FocusInfo, error) {
	return foregroundImpl()
}

// IdleDuration returns how long the user has been idle (no keyboard or mouse
// input). Returns 0 on the lock screen or when the system cannot determine
// idle time.
func IdleDuration() (time.Duration, error) {
	return idleDurationImpl()
}

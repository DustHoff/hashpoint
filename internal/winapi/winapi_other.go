//go:build !windows

package winapi

import "time"

// Stub implementations so the package compiles on non-Windows hosts (linting).
// They never run in production.

func foregroundImpl() (FocusInfo, error) {
	return FocusInfo{}, ErrUnsupported
}

func idleDurationImpl() (time.Duration, error) {
	return 0, ErrUnsupported
}

func enumVisibleWindowsForProcessesImpl(_ []string) ([]WindowInfo, error) {
	return nil, ErrUnsupported
}

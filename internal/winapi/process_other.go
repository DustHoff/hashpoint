//go:build !windows

package winapi

import "time"

// CurrentProcessCreationTime is unavailable on non-Windows builds (used only
// for Linux CI/linting); callers fall back to a wall-clock reading.
func CurrentProcessCreationTime() (time.Time, error) { return time.Time{}, ErrUnsupported }

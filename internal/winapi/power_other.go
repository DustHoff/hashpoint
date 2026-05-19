//go:build !windows

package winapi

import "log/slog"

// PowerMonitor is a no-op stub on non-Windows platforms. The host is
// Windows-only; this stub keeps the package importable from any GOOS used
// for linting and unit tests.
type PowerMonitor struct{}

// NewPowerMonitor is a no-op on non-Windows platforms — it returns a stub
// monitor that never fires.
func NewPowerMonitor(_ *slog.Logger, _, _ func()) (*PowerMonitor, error) {
	return &PowerMonitor{}, nil
}

// Close is a no-op.
func (*PowerMonitor) Close() error { return nil }

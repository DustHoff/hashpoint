//go:build !windows

package main

import "errors"

// runWatchdog is unavailable off Windows: the watchdog is hosted as a Windows
// service. This stub keeps cmd/timetracker building on the Linux runners used
// for linting and tests.
func runWatchdog() error {
	return errors.New("watchdog mode is only supported on Windows")
}

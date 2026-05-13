//go:build !windows

package main

import (
	"context"

	"github.com/dusthoff/hashpoint/internal/app"
	"github.com/dusthoff/hashpoint/internal/personio"
)

func defaultSessionStore() personio.SessionStore {
	return personio.NewMemorySessionStore()
}

// runTray is a no-op on non-Windows builds — used only by linting on Linux CI.
func runTray(_ context.Context, _ *app.App, _ string) {}

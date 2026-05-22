//go:build !windows

package ipc

import (
	"context"
	"errors"
	"net"
)

// errUnsupportedPlatform is returned by the pipe transport on non-Windows
// builds. The collector/UI split is Windows-only; these stubs keep the package
// compilable for Linux lint/CI runs.
var errUnsupportedPlatform = errors.New("ipc: named-pipe transport is Windows-only")

func listenPipe(string) (net.Listener, error) { return nil, errUnsupportedPlatform }

func dialPipe(context.Context, string) (net.Conn, error) { return nil, errUnsupportedPlatform }

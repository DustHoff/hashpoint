//go:build windows

package ipc

import (
	"context"
	"fmt"
	"net"
	"os/user"

	"github.com/Microsoft/go-winio"
)

// listenPipe opens a named-pipe listener restricted to the current user and
// LocalSystem via a protected DACL, so no other local account can connect —
// the channel carries Personio/Entra session control, so an open pipe would
// be a privilege-escalation surface (ADR §4).
func listenPipe(name string) (net.Listener, error) {
	sddl, err := userOnlySDDL()
	if err != nil {
		return nil, err
	}
	l, err := winio.ListenPipe(name, &winio.PipeConfig{SecurityDescriptor: sddl})
	if err != nil {
		return nil, fmt.Errorf("listen pipe %s: %w", name, err)
	}
	return l, nil
}

// dialPipe connects to the collector's named pipe, honouring ctx for the
// connect timeout and waiting briefly if the pipe instance is momentarily
// busy.
func dialPipe(ctx context.Context, name string) (net.Conn, error) {
	c, err := winio.DialPipeContext(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("dial pipe %s: %w", name, err)
	}
	return c, nil
}

// userOnlySDDL builds a protected-DACL security descriptor granting full
// access to the owning user and LocalSystem only. user.Current().Uid is the
// account SID string on Windows.
func userOnlySDDL() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("current user: %w", err)
	}
	return fmt.Sprintf("D:P(A;;GA;;;%s)(A;;GA;;;SY)", u.Uid), nil
}

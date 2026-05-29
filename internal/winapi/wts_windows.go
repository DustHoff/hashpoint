//go:build windows

package winapi

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// invalidSessionID is the sentinel WTSGetActiveConsoleSessionId returns when no
// session is attached to the physical console.
const invalidSessionID = 0xFFFFFFFF

// ActiveConsoleSessionID returns the Windows session ID of the user physically
// at the console, or ErrNoActiveSession when no interactive user is logged on
// (the API returns 0xFFFFFFFF in that case). A service runs in Session 0 and
// cannot see the interactive session's objects, so it must resolve the session
// explicitly before acting on a per-user process.
func ActiveConsoleSessionID() (uint32, error) {
	id := windows.WTSGetActiveConsoleSessionId()
	if id == invalidSessionID {
		return 0, ErrNoActiveSession
	}
	return id, nil
}

// UserToken returns a primary token for the interactive user in sessionID. The
// caller owns the returned token and must Close it. The call requires
// SeTcbPrivilege, which the LocalSystem account running the watchdog holds.
func UserToken(sessionID uint32) (windows.Token, error) {
	var tok windows.Token
	if err := windows.WTSQueryUserToken(sessionID, &tok); err != nil {
		return 0, fmt.Errorf("WTSQueryUserToken(session=%d): %w", sessionID, err)
	}
	return tok, nil
}

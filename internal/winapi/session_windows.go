//go:build windows

package winapi

import (
	"errors"
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// desktopReadObjects is the minimal access right needed to open the current
// input desktop for a name query; uoiName selects the desktop name in
// GetUserObjectInformationW.
const (
	desktopReadObjects = 0x0001
	uoiName            = 2 // UOI_NAME
)

// modUser32 is declared in winapi_windows.go; reuse it for the desktop calls.
var (
	procOpenInputDesktop          = modUser32.NewProc("OpenInputDesktop")
	procCloseDesktop              = modUser32.NewProc("CloseDesktop")
	procGetUserObjectInformationW = modUser32.NewProc("GetUserObjectInformationW")
)

// SessionLocked reports whether the interactive console session is currently on
// a locked or secure desktop — the lock screen, the Winlogon/UAC secure
// desktop, or a secure screensaver. The collector uses this to avoid spawning
// the WebView2 UI onto a desktop where it cannot initialize and would
// crash-loop after a Modern-Standby resume (issue #21). The headless collector
// itself runs fine while locked, so only the UI spawn is gated on this.
//
// It inspects the desktop that currently receives user input: when the session
// is unlocked that desktop is named "Default"; when locked, the input desktop is
// the SYSTEM-owned secure desktop, which an interactive process cannot open —
// that failure is itself treated as "locked".
func SessionLocked() (bool, error) {
	hdesk, _, callErr := procOpenInputDesktop.Call(0, 0, uintptr(desktopReadObjects))
	if hdesk == 0 {
		// The lock/secure desktop is owned by SYSTEM; an interactive process is
		// denied access — that specific failure is what "locked" looks like. Any
		// other failure is unexpected, so surface it as an error and let the
		// caller fail open (allow the spawn) rather than hold the UI back.
		if errors.Is(callErr, windows.ERROR_ACCESS_DENIED) {
			return true, nil
		}
		return false, fmt.Errorf("OpenInputDesktop: %w", callErr)
	}
	defer procCloseDesktop.Call(hdesk) //nolint:errcheck // best-effort handle close

	name, err := inputDesktopName(windows.Handle(hdesk))
	if err != nil {
		return false, err
	}
	return !strings.EqualFold(name, "Default"), nil
}

// inputDesktopName returns the UOI_NAME of the given desktop handle (e.g.
// "Default", "Winlogon", "Screen-saver").
func inputDesktopName(h windows.Handle) (string, error) {
	buf := make([]uint16, 256)
	var needed uint32
	r, _, err := procGetUserObjectInformationW.Call(
		uintptr(h),
		uintptr(uoiName),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)*2),
		uintptr(unsafe.Pointer(&needed)),
	)
	if r == 0 {
		return "", fmt.Errorf("GetUserObjectInformationW: %w", err)
	}
	return windows.UTF16ToString(buf), nil
}

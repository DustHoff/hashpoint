//go:build windows

package winapi

import (
	"fmt"
	"path/filepath"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The bulk of the Win32 surface is reached via golang.org/x/sys/windows
// (GetForegroundWindow, GetWindowThreadProcessId, OpenProcess,
// QueryFullProcessImageName, CloseHandle). The four calls below have no typed
// wrapper in x/sys/windows yet, so we resolve them once at init via the
// (lazy) procedure table x/sys/windows itself maintains for those DLLs.
var (
	modUser32   = windows.NewLazySystemDLL("user32.dll")
	modKernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procGetWindowTextW       = modUser32.NewProc("GetWindowTextW")
	procGetWindowTextLengthW = modUser32.NewProc("GetWindowTextLengthW")
	procGetLastInputInfo     = modUser32.NewProc("GetLastInputInfo")
	procGetTickCount         = modKernel32.NewProc("GetTickCount")
)

type lastInputInfo struct {
	cbSize uint32
	dwTime uint32
}

func foregroundImpl() (FocusInfo, error) {
	hwnd := windows.GetForegroundWindow()
	if hwnd == 0 {
		return FocusInfo{}, nil
	}
	title, err := windowText(hwnd)
	if err != nil {
		return FocusInfo{}, fmt.Errorf("get window text: %w", err)
	}
	pid, err := windowProcessID(hwnd)
	if err != nil {
		return FocusInfo{}, fmt.Errorf("get pid: %w", err)
	}
	path := processPath(pid)
	name := filepath.Base(path)
	return FocusInfo{
		HWND:        uintptr(hwnd),
		PID:         pid,
		Title:       title,
		ProcessPath: path,
		ProcessName: name,
	}, nil
}

func windowText(hwnd windows.HWND) (string, error) {
	n, _, _ := procGetWindowTextLengthW.Call(uintptr(hwnd))
	if n == 0 {
		return "", nil
	}
	buf := make([]uint16, int(n)+1)
	r, _, _ := procGetWindowTextW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if r == 0 {
		// Empty/unreadable title is not an error — return empty string.
		return "", nil
	}
	return windows.UTF16ToString(buf), nil
}

func windowProcessID(hwnd windows.HWND) (uint32, error) {
	var pid uint32
	if _, err := windows.GetWindowThreadProcessId(hwnd, &pid); err != nil {
		return 0, fmt.Errorf("GetWindowThreadProcessId: %w", err)
	}
	return pid, nil
}

func processPath(pid uint32) string {
	if pid == 0 {
		return ""
	}
	const PROCESS_QUERY_LIMITED_INFORMATION = 0x1000
	h, err := windows.OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil || h == 0 {
		return ""
	}
	defer windows.CloseHandle(h)

	buf := make([]uint16, windows.MAX_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:size])
}

func idleDurationImpl() (time.Duration, error) {
	var lii lastInputInfo
	lii.cbSize = uint32(unsafe.Sizeof(lii))
	r, _, e := procGetLastInputInfo.Call(uintptr(unsafe.Pointer(&lii)))
	if r == 0 {
		return 0, fmt.Errorf("GetLastInputInfo: %v", e)
	}
	tick, _, _ := procGetTickCount.Call()
	if uint32(tick) < lii.dwTime {
		return 0, nil
	}
	return time.Duration(uint32(tick)-lii.dwTime) * time.Millisecond, nil
}

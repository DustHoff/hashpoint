//go:build windows

package winapi

import (
	"fmt"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procGetForegroundWindow     = user32.NewProc("GetForegroundWindow")
	procGetWindowTextW          = user32.NewProc("GetWindowTextW")
	procGetWindowTextLengthW    = user32.NewProc("GetWindowTextLengthW")
	procGetWindowThreadProcID   = user32.NewProc("GetWindowThreadProcessId")
	procGetLastInputInfo        = user32.NewProc("GetLastInputInfo")
	procGetTickCount            = kernel32.NewProc("GetTickCount")
	procQueryFullProcessImageNW = kernel32.NewProc("QueryFullProcessImageNameW")
)

type lastInputInfo struct {
	cbSize uint32
	dwTime uint32
}

func foregroundImpl() (FocusInfo, error) {
	hwnd, _, _ := procGetForegroundWindow.Call()
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
		HWND:        hwnd,
		PID:         pid,
		Title:       title,
		ProcessPath: path,
		ProcessName: name,
	}, nil
}

func windowText(hwnd uintptr) (string, error) {
	n, _, _ := procGetWindowTextLengthW.Call(hwnd)
	if n == 0 {
		return "", nil
	}
	buf := make([]uint16, int(n)+1)
	r, _, e := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if r == 0 {
		// Empty title is not an error; fall through with empty string.
		if e == nil || e.(syscall.Errno) == 0 {
			return "", nil
		}
	}
	return windows.UTF16ToString(buf), nil
}

func windowProcessID(hwnd uintptr) (uint32, error) {
	var pid uint32
	r, _, e := procGetWindowThreadProcID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if r == 0 {
		return 0, fmt.Errorf("GetWindowThreadProcessId: %v", e)
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
	r, _, _ := procQueryFullProcessImageNW.Call(uintptr(h), 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if r == 0 {
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

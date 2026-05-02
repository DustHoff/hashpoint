//go:build windows

package winapi

import (
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"
)

// Win32 message and constant codes used by the hotkey + monitor helpers.
// Intentionally local to this file — the public surface is HotkeyManager
// + the Rect/CursorMonitorWorkArea helper.
const (
	wmHotkey      uint32 = 0x0312
	wmAppRegister uint32 = 0x8000 // WM_APP — "registration parameters changed"
	wmQuit        uint32 = 0x0012

	monitorDefaultToNearest = 2

	hotkeyID = 1
)

var (
	procRegisterHotKey     = modUser32.NewProc("RegisterHotKey")
	procUnregisterHotKey   = modUser32.NewProc("UnregisterHotKey")
	procGetMessageW        = modUser32.NewProc("GetMessageW")
	procPeekMessageW       = modUser32.NewProc("PeekMessageW")
	procPostThreadMessageW = modUser32.NewProc("PostThreadMessageW")
	procGetCurrentThreadId = modKernel32.NewProc("GetCurrentThreadId")
	procGetCursorPos       = modUser32.NewProc("GetCursorPos")
	procMonitorFromPoint   = modUser32.NewProc("MonitorFromPoint")
	procGetMonitorInfoW    = modUser32.NewProc("GetMonitorInfoW")
)

type winMsg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      winPoint
	private uint32
}

type winPoint struct {
	x, y int32
}

type winRect struct {
	left, top, right, bottom int32
}

type monitorInfo struct {
	cbSize    uint32
	rcMonitor winRect
	rcWork    winRect
	flags     uint32
}

// Rect is a screen-coordinate rectangle in physical pixels.
type Rect struct {
	Left, Top, Right, Bottom int32
}

// Width returns the rectangle's width in pixels.
func (r Rect) Width() int32 { return r.Right - r.Left }

// Height returns the rectangle's height in pixels.
func (r Rect) Height() int32 { return r.Bottom - r.Top }

// CursorMonitorWorkArea returns the work area (screen minus taskbar/docked
// bars) of the monitor that currently contains the mouse cursor. Used by
// the quick-tag-picker to anchor itself to the bottom-right of the same
// display the user is interacting with.
func CursorMonitorWorkArea() (Rect, error) {
	var p winPoint
	r, _, e := procGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	if r == 0 {
		return Rect{}, fmt.Errorf("GetCursorPos: %v", e)
	}
	h, _, _ := procMonitorFromPoint.Call(uintptr(p.x), uintptr(p.y), monitorDefaultToNearest)
	if h == 0 {
		return Rect{}, errors.New("MonitorFromPoint returned NULL")
	}
	var mi monitorInfo
	mi.cbSize = uint32(unsafe.Sizeof(mi))
	r, _, e = procGetMonitorInfoW.Call(h, uintptr(unsafe.Pointer(&mi)))
	if r == 0 {
		return Rect{}, fmt.Errorf("GetMonitorInfoW: %v", e)
	}
	return Rect{
		Left:   mi.rcWork.left,
		Top:    mi.rcWork.top,
		Right:  mi.rcWork.right,
		Bottom: mi.rcWork.bottom,
	}, nil
}

// HotkeyManager owns a single Win32 global hotkey registration. Because
// RegisterHotKey associates the registration with the calling thread, all
// register/unregister/GetMessage calls happen on a dedicated OS-locked
// thread. SetHotkey is safe to call from any goroutine — it posts a custom
// message that the loop thread picks up and acts on.
//
// The fire callback is invoked from its own goroutine so it can call back
// into the application without blocking the message pump.
type HotkeyManager struct {
	logger *slog.Logger

	startOnce sync.Once
	startErr  error
	done      chan struct{}

	threadIDMu sync.Mutex
	threadID   uint32

	cb atomic.Pointer[func()]

	regMu sync.Mutex
	reg   hotkeyReg
}

type hotkeyReg struct {
	enabled bool
	mods    uint32
	vk      uint32
}

// NewHotkeyManager returns a not-yet-started manager. Call Start before
// the first SetHotkey.
func NewHotkeyManager(logger *slog.Logger) *HotkeyManager {
	if logger == nil {
		logger = slog.Default()
	}
	return &HotkeyManager{logger: logger}
}

// Start launches the message-pump goroutine. Idempotent.
func (m *HotkeyManager) Start() error {
	m.startOnce.Do(func() {
		ready := make(chan uint32, 1)
		m.done = make(chan struct{})
		go m.run(ready)
		tid := <-ready
		if tid == 0 {
			m.startErr = errors.New("hotkey: message-loop thread failed to start")
			return
		}
		m.threadIDMu.Lock()
		m.threadID = tid
		m.threadIDMu.Unlock()
	})
	return m.startErr
}

// SetHotkey atomically replaces the current hotkey registration. Pass
// enabled=false to unregister entirely. Returns an error only when the
// manager is not running.
func (m *HotkeyManager) SetHotkey(enabled bool, mods, vk uint32, onFire func()) error {
	m.threadIDMu.Lock()
	tid := m.threadID
	m.threadIDMu.Unlock()
	if tid == 0 {
		return errors.New("hotkey: manager not started")
	}
	m.regMu.Lock()
	m.reg = hotkeyReg{enabled: enabled, mods: mods, vk: vk}
	m.regMu.Unlock()
	if enabled && onFire != nil {
		fn := onFire
		m.cb.Store(&fn)
	} else {
		m.cb.Store(nil)
	}
	return m.postThread(tid, wmAppRegister, 0, 0)
}

// Stop tears the message-loop thread down. Subsequent SetHotkey calls
// return an error.
func (m *HotkeyManager) Stop() {
	m.threadIDMu.Lock()
	tid := m.threadID
	m.threadID = 0
	m.threadIDMu.Unlock()
	if tid == 0 {
		return
	}
	_ = m.postThread(tid, wmQuit, 0, 0)
	if m.done != nil {
		<-m.done
	}
}

func (m *HotkeyManager) postThread(tid uint32, msg uint32, wParam, lParam uintptr) error {
	r, _, e := procPostThreadMessageW.Call(uintptr(tid), uintptr(msg), wParam, lParam)
	if r == 0 {
		return fmt.Errorf("PostThreadMessageW: %v", e)
	}
	return nil
}

func (m *HotkeyManager) run(ready chan<- uint32) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(m.done)

	tid, _, _ := procGetCurrentThreadId.Call()
	if tid == 0 {
		ready <- 0
		return
	}
	// Force the system to allocate a message queue for this thread before
	// we signal readiness — otherwise PostThreadMessage from SetHotkey
	// could fail with ERROR_INVALID_THREAD_ID. PeekMessage with PM_NOREMOVE
	// returns immediately and is the canonical idiom.
	var probe winMsg
	procPeekMessageW.Call(uintptr(unsafe.Pointer(&probe)), 0, 0, 0, 0)
	ready <- uint32(tid)

	registered := false
	for {
		var msg winMsg
		r, _, e := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) == -1 {
			m.logger.Error("hotkey: GetMessageW failed", "err", e)
			break
		}
		if r == 0 {
			// WM_QUIT
			break
		}
		switch msg.message {
		case wmHotkey:
			if cb := m.cb.Load(); cb != nil {
				go (*cb)()
			}
		case wmAppRegister:
			m.regMu.Lock()
			req := m.reg
			m.regMu.Unlock()
			if registered {
				procUnregisterHotKey.Call(0, hotkeyID)
				registered = false
			}
			if req.enabled {
				r, _, e := procRegisterHotKey.Call(0, hotkeyID, uintptr(req.mods), uintptr(req.vk))
				if r == 0 {
					m.logger.Warn("hotkey: RegisterHotKey failed",
						"mods", req.mods, "vk", req.vk, "err", e)
				} else {
					registered = true
					m.logger.Info("hotkey: registered", "mods", req.mods, "vk", req.vk)
				}
			}
		}
	}
	if registered {
		procUnregisterHotKey.Call(0, hotkeyID)
	}
}

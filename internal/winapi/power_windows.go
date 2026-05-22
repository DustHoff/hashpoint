//go:build windows

package winapi

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Power-broadcast event codes the monitor surfaces to the host. The full
// PBT_* set is much larger; we only react to the suspend/resume pair since
// that's what kills the WebView2 host process during Modern Standby.
const (
	pbtAPMSuspend         = 0x0004
	pbtAPMResumeAutomatic = 0x0012
	pbtAPMResumeSuspend   = 0x0007

	deviceNotifyCallback = 2
)

var (
	modPowrprof = windows.NewLazySystemDLL("powrprof.dll")

	procPowerRegisterSuspendResumeNotification   = modPowrprof.NewProc("PowerRegisterSuspendResumeNotification")
	procPowerUnregisterSuspendResumeNotification = modPowrprof.NewProc("PowerUnregisterSuspendResumeNotification")
)

// deviceNotifySubscribeParameters mirrors the Win32 struct of the same name.
// The Callback field holds a stdcall thunk produced by windows.NewCallback.
type deviceNotifySubscribeParameters struct {
	Callback uintptr
	Context  uintptr
}

// PowerMonitor receives PBT_APMSUSPEND/PBT_APMRESUMEAUTOMATIC notifications
// from Windows and invokes the supplied callbacks. The host can use these
// to flush state before the system suspends and to resume tracking after a
// wake — both critical because Modern Standby frequently kills the
// WebView2 host process while the system is suspended, leaving Hashpoint
// without a chance to run its normal Wails OnShutdown path.
type PowerMonitor struct {
	logger *slog.Logger

	mu        sync.Mutex
	handle    uintptr
	onSuspend func()
	onResume  func()
	cbThunk   uintptr
	// params must outlive the registration: Windows holds a pointer to it
	// for the lifetime of the subscription, so we pin it via the receiver.
	params deviceNotifySubscribeParameters
}

// NewPowerMonitor registers for suspend/resume notifications. The callbacks
// run on a Windows-owned notification thread — they must hand off work to
// a goroutine and return quickly. Passing nil for either callback disables
// the corresponding edge.
func NewPowerMonitor(logger *slog.Logger, onSuspend, onResume func()) (*PowerMonitor, error) {
	if logger == nil {
		logger = slog.Default()
	}
	m := &PowerMonitor{logger: logger, onSuspend: onSuspend, onResume: onResume}
	m.cbThunk = windows.NewCallback(m.dispatch)
	m.params = deviceNotifySubscribeParameters{Callback: m.cbThunk}

	var handle uintptr
	r, _, e := procPowerRegisterSuspendResumeNotification.Call(
		uintptr(deviceNotifyCallback),
		uintptr(unsafe.Pointer(&m.params)),
		uintptr(unsafe.Pointer(&handle)),
	)
	// PowerRegisterSuspendResumeNotification returns ERROR_SUCCESS (0) on
	// success; anything else is a real failure. The errno returned by Call
	// is the last-error code, which is not authoritative for this API — we
	// rely on the return value instead.
	if r != 0 {
		return nil, fmt.Errorf("PowerRegisterSuspendResumeNotification: code=%d errno=%v", r, e)
	}
	m.handle = handle
	return m, nil
}

// dispatch is the C ABI callback Windows calls on its notification thread.
// It must return promptly; we offload application-level work to goroutines
// so the notification thread is never blocked by Go-side logic.
func (m *PowerMonitor) dispatch(_, msgType, _ uintptr) uintptr {
	switch uint32(msgType) {
	case pbtAPMSuspend:
		m.logger.Info("power: suspend notification received")
		if cb := m.onSuspend; cb != nil {
			go cb()
		}
	case pbtAPMResumeAutomatic, pbtAPMResumeSuspend:
		m.logger.Info("power: resume notification received", "type", uint32(msgType))
		if cb := m.onResume; cb != nil {
			go cb()
		}
	}
	return 0
}

// Close unregisters the notification. Safe to call multiple times. The
// thunk created by windows.NewCallback is intentionally not freed — the
// runtime has no API for releasing it, and the cost of one leaked thunk
// per process lifetime is negligible.
func (m *PowerMonitor) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.handle == 0 {
		return nil
	}
	r, _, e := procPowerUnregisterSuspendResumeNotification.Call(m.handle)
	m.handle = 0
	if r != 0 {
		// Promote the errno only when it's a meaningful Win32 code.
		var errno syscall.Errno
		if errors.As(e, &errno) && errno != 0 {
			return fmt.Errorf("PowerUnregisterSuspendResumeNotification: code=%d errno=%v", r, errno)
		}
		return fmt.Errorf("PowerUnregisterSuspendResumeNotification: code=%d", r)
	}
	return nil
}

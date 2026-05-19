//go:build windows

package winapi

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ErrAlreadyRunning is returned by AcquireSingleInstanceLock when another
// instance of the application already holds the named mutex.
var ErrAlreadyRunning = errors.New("winapi: another instance already running")

var procCreateMutexW = modKernel32.NewProc("CreateMutexW")

// SingleInstanceLock owns a Windows named mutex used to guarantee that only
// one Hashpoint process runs per user session. Release on shutdown to free
// the kernel handle.
type SingleInstanceLock struct {
	handle windows.Handle
}

// AcquireSingleInstanceLock creates (or opens) a session-local named mutex.
// If the mutex already exists, another process is holding it — the helper
// closes its duplicate handle and returns ErrAlreadyRunning. The "Local\"
// prefix scopes the mutex to the current login session, so multiple users
// on the same machine each get their own instance.
//
// Why a named mutex and not a file lock: a mutex's lifetime is bound to its
// owning process; an abrupt termination (the very situation we're guarding
// against here) releases the handle automatically. A file lock would
// require explicit cleanup that we know we cannot rely on.
func AcquireSingleInstanceLock(name string) (*SingleInstanceLock, error) {
	if name == "" {
		return nil, errors.New("winapi: empty single-instance name")
	}
	qualified := `Local\` + name
	n, err := windows.UTF16PtrFromString(qualified)
	if err != nil {
		return nil, fmt.Errorf("encode name: %w", err)
	}
	// CreateMutexW(lpMutexAttributes=NULL, bInitialOwner=FALSE, lpName).
	// LazyProc.Call always returns the last-error errno; that errno is
	// ERROR_ALREADY_EXISTS when another process holds the same name, even
	// though we still get back a valid handle.
	r, _, e := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(n)))
	if r == 0 {
		return nil, fmt.Errorf("CreateMutexW: %w", e)
	}
	h := windows.Handle(r)
	if errors.Is(e, syscall.Errno(windows.ERROR_ALREADY_EXISTS)) {
		_ = windows.CloseHandle(h)
		return nil, ErrAlreadyRunning
	}
	return &SingleInstanceLock{handle: h}, nil
}

// Release closes the underlying kernel handle. Safe to call multiple times.
func (l *SingleInstanceLock) Release() error {
	if l == nil || l.handle == 0 {
		return nil
	}
	err := windows.CloseHandle(l.handle)
	l.handle = 0
	return err
}

//go:build windows

package winapi

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	// processQueryLimitedInformation is PROCESS_QUERY_LIMITED_INFORMATION; the
	// least-privileged access right that still allows GetExitCodeProcess,
	// QueryFullProcessImageName and GetProcessTimes.
	processQueryLimitedInformation = 0x1000
	// stillActive is STILL_ACTIVE — the exit code GetExitCodeProcess reports for
	// a process that has not exited.
	stillActive = 259
	// creationTimeTolerance bounds how far a live process's creation time may
	// differ from the marker's recorded start before we treat the PID as
	// reused (a different process now owning the old PID).
	creationTimeTolerance = 5 * time.Second
)

// ProcessLiveness reports whether pid identifies a live process that is still
// the one described by the marker. It guards against PID reuse: a dead pid, an
// image-name mismatch, or a creation time that differs from wantCreate by more
// than creationTimeTolerance all yield alive=false. wantImage is the expected
// executable base name (empty skips that guard); wantCreate is the marker's
// start time (zero skips that guard). A pid that cannot be opened is treated as
// dead, not as an error — the usual reason is that it has already exited.
func ProcessLiveness(pid int, wantImage string, wantCreate time.Time) (bool, error) {
	if pid <= 0 {
		return false, nil
	}
	h, err := windows.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		// ERROR_INVALID_PARAMETER for a vanished pid; anything else we also
		// treat as "not a live collector" rather than a hard failure.
		return false, nil
	}
	defer func() { _ = windows.CloseHandle(h) }()

	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false, fmt.Errorf("GetExitCodeProcess: %w", err)
	}
	if code != stillActive {
		return false, nil
	}

	if wantImage != "" && !imageMatches(h, wantImage) {
		return false, nil
	}
	if !wantCreate.IsZero() && !creationMatches(h, wantCreate) {
		return false, nil
	}
	return true, nil
}

// imageMatches reports whether the process image base name (case-insensitive)
// equals want. A query failure is treated as a match so a transient error does
// not suppress a needed liveness signal.
func imageMatches(h windows.Handle, want string) bool {
	buf := make([]uint16, windows.MAX_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return true
	}
	got := filepath.Base(windows.UTF16ToString(buf[:size]))
	return strings.EqualFold(got, want)
}

// creationMatches reports whether the process creation time is within
// creationTimeTolerance of want. A query failure is treated as a match.
func creationMatches(h windows.Handle, want time.Time) bool {
	var create, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &create, &exit, &kernel, &user); err != nil {
		return true
	}
	got := time.Unix(0, create.Nanoseconds()).UTC()
	diff := got.Sub(want)
	if diff < 0 {
		diff = -diff
	}
	return diff <= creationTimeTolerance
}

// LaunchAsUser starts exe with args in the interactive session owned by token,
// using an environment block derived from the user's profile (so the child's
// %LOCALAPPDATA% etc. resolve correctly) and the interactive desktop. The child
// runs detached. Returns the new process ID. The token typically comes from
// ActiveConsoleSessionID + UserToken.
func LaunchAsUser(token windows.Token, exe string, args []string, workDir string) (uint32, error) {
	var envBlock *uint16
	r, _, e := procCreateEnvironmentBlock.Call(
		uintptr(unsafe.Pointer(&envBlock)),
		uintptr(token),
		0, // bInherit = FALSE
	)
	if r == 0 {
		return 0, fmt.Errorf("CreateEnvironmentBlock: %w", e)
	}
	defer func() { _, _, _ = procDestroyEnvironmentBlock.Call(uintptr(unsafe.Pointer(envBlock))) }()

	appName, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return 0, fmt.Errorf("encode exe path: %w", err)
	}
	cmdline, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(append([]string{exe}, args...)))
	if err != nil {
		return 0, fmt.Errorf("encode command line: %w", err)
	}
	desktop, err := windows.UTF16PtrFromString(`winsta0\default`)
	if err != nil {
		return 0, fmt.Errorf("encode desktop: %w", err)
	}
	var cwd *uint16
	if workDir != "" {
		if cwd, err = windows.UTF16PtrFromString(workDir); err != nil {
			return 0, fmt.Errorf("encode work dir: %w", err)
		}
	}

	si := windows.StartupInfo{Desktop: desktop}
	si.Cb = uint32(unsafe.Sizeof(si))
	var pi windows.ProcessInformation

	const flags = windows.CREATE_UNICODE_ENVIRONMENT | windows.DETACHED_PROCESS
	if err := windows.CreateProcessAsUser(
		token, appName, cmdline,
		nil, nil, false, flags,
		envBlock, cwd, &si, &pi,
	); err != nil {
		return 0, fmt.Errorf("CreateProcessAsUser: %w", err)
	}
	_ = windows.CloseHandle(pi.Thread)
	_ = windows.CloseHandle(pi.Process)
	return pi.ProcessId, nil
}

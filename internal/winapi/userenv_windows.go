//go:build windows

package winapi

import (
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modUserenv = windows.NewLazySystemDLL("userenv.dll")

	procCreateEnvironmentBlock  = modUserenv.NewProc("CreateEnvironmentBlock")
	procDestroyEnvironmentBlock = modUserenv.NewProc("DestroyEnvironmentBlock")
)

// UserEnvironment returns the environment block for token as a name→value map,
// built via CreateEnvironmentBlock so it reflects the user's real profile
// (e.g. LOCALAPPDATA / USERPROFILE) rather than the service account's. This is
// how the LocalSystem watchdog resolves the interactive user's data directory.
// Inheritance from the current process environment is disabled.
func UserEnvironment(token windows.Token) (map[string]string, error) {
	var block *uint16
	r, _, e := procCreateEnvironmentBlock.Call(
		uintptr(unsafe.Pointer(&block)),
		uintptr(token),
		0, // bInherit = FALSE
	)
	if r == 0 {
		return nil, fmt.Errorf("CreateEnvironmentBlock: %w", e)
	}
	defer func() { _, _, _ = procDestroyEnvironmentBlock.Call(uintptr(unsafe.Pointer(block))) }()

	return parseEnvBlock(block), nil
}

// parseEnvBlock walks a double-NUL-terminated UTF-16 environment block (as
// produced by CreateEnvironmentBlock) into a map. Each entry is "NAME=VALUE";
// an empty string terminates the block. unsafe.Add keeps the pointer
// arithmetic vet-clean (no uintptr round-trip).
func parseEnvBlock(block *uint16) map[string]string {
	out := make(map[string]string)
	if block == nil {
		return out
	}
	p := unsafe.Pointer(block)
	for {
		n := 0
		for *(*uint16)(unsafe.Add(p, n*2)) != 0 {
			n++
		}
		if n == 0 {
			break // empty string marks the end of the block
		}
		s := windows.UTF16ToString(unsafe.Slice((*uint16)(p), n))
		if eq := strings.IndexByte(s, '='); eq > 0 {
			out[s[:eq]] = s[eq+1:]
		}
		p = unsafe.Add(p, (n+1)*2) // advance past the string and its NUL
	}
	return out
}

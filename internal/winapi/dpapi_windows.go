//go:build windows

package winapi

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// DPAPI surface used by the Entra ID token cache.
//
// We deliberately bind to CurrentUser scope (no CRYPTPROTECT_LOCAL_MACHINE)
// so the encrypted cache file can only be decrypted by the same Windows
// user account that wrote it. UI prompts are forbidden — any prompt-worthy
// failure (re-encryption denied, cred-store unreachable on first call) is
// surfaced as a Go error and the caller falls back to "no cached token".
var (
	modCrypt32 = windows.NewLazySystemDLL("crypt32.dll")

	procCryptProtectData   = modCrypt32.NewProc("CryptProtectData")
	procCryptUnprotectData = modCrypt32.NewProc("CryptUnprotectData")
	procLocalFree          = modKernel32.NewProc("LocalFree")
)

// dpapiBlob mirrors the DATA_BLOB struct from wincrypt.h.
type dpapiBlob struct {
	cbData uint32
	pbData *byte
}

func newDPAPIBlob(b []byte) dpapiBlob {
	if len(b) == 0 {
		return dpapiBlob{}
	}
	return dpapiBlob{
		cbData: uint32(len(b)),
		pbData: &b[0],
	}
}

// goBytes copies the LocalAlloc-owned buffer into a Go-managed slice; the
// caller frees the original via LocalFree (typically in a deferred call).
func (b *dpapiBlob) goBytes() []byte {
	if b.cbData == 0 || b.pbData == nil {
		return nil
	}
	src := unsafe.Slice(b.pbData, int(b.cbData))
	out := make([]byte, len(src))
	copy(out, src)
	return out
}

const cryptProtectUIForbidden = 0x1

// ProtectDataCurrentUser encrypts plaintext using DPAPI bound to the current
// Windows user account. The returned ciphertext is opaque, version-tagged
// and integrity-protected by Windows; it can only be decrypted by the same
// user on the same machine (roaming profiles included). Returns an error
// for empty inputs and for any underlying CryptProtectData failure.
func ProtectDataCurrentUser(plaintext []byte) ([]byte, error) {
	if len(plaintext) == 0 {
		return nil, errors.New("winapi: empty plaintext")
	}
	in := newDPAPIBlob(plaintext)
	var out dpapiBlob
	ret, _, callErr := procCryptProtectData.Call(
		uintptr(unsafe.Pointer(&in)),
		0, // szDataDescr — we don't tag the blob with a description
		0, // pOptionalEntropy — keyed solely on the user identity
		0, // pvReserved
		0, // pPromptStruct
		cryptProtectUIForbidden,
		uintptr(unsafe.Pointer(&out)),
	)
	if ret == 0 {
		return nil, fmt.Errorf("CryptProtectData: %w", callErr)
	}
	defer func() {
		_, _, _ = procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	}()
	return out.goBytes(), nil
}

// UnprotectDataCurrentUser decrypts a blob previously written by
// ProtectDataCurrentUser. Returns an error (which the caller treats as
// "cache invalid, log in again") when the blob came from a different
// user/machine, has been tampered with, or the user's master key is
// otherwise unreadable.
func UnprotectDataCurrentUser(ciphertext []byte) ([]byte, error) {
	if len(ciphertext) == 0 {
		return nil, errors.New("winapi: empty ciphertext")
	}
	in := newDPAPIBlob(ciphertext)
	var out dpapiBlob
	ret, _, callErr := procCryptUnprotectData.Call(
		uintptr(unsafe.Pointer(&in)),
		0, // ppszDataDescr — discarded
		0, // pOptionalEntropy
		0, // pvReserved
		0, // pPromptStruct
		cryptProtectUIForbidden,
		uintptr(unsafe.Pointer(&out)),
	)
	if ret == 0 {
		return nil, fmt.Errorf("CryptUnprotectData: %w", callErr)
	}
	defer func() {
		_, _, _ = procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	}()
	return out.goBytes(), nil
}

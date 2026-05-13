//go:build !windows

package winapi

// ProtectDataCurrentUser is a stub for non-Windows builds (lint hosts).
func ProtectDataCurrentUser(_ []byte) ([]byte, error) { return nil, ErrUnsupported }

// UnprotectDataCurrentUser is a stub for non-Windows builds (lint hosts).
func UnprotectDataCurrentUser(_ []byte) ([]byte, error) { return nil, ErrUnsupported }

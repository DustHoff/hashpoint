//go:build windows

package config

import (
	"errors"
	"os"
)

// userConfigRoot returns %APPDATA% on Windows.
func userConfigRoot() (string, error) {
	if v := os.Getenv("APPDATA"); v != "" {
		return v, nil
	}
	return "", errors.New("APPDATA not set")
}

// userDataRoot returns %LOCALAPPDATA% on Windows.
func userDataRoot() (string, error) {
	if v := os.Getenv("LOCALAPPDATA"); v != "" {
		return v, nil
	}
	return "", errors.New("LOCALAPPDATA not set")
}

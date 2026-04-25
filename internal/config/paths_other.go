//go:build !windows

package config

import (
	"os"
	"path/filepath"
)

// userConfigRoot returns ~/.config on non-Windows builds (mainly to keep CI
// linting compilable on Linux runners).
func userConfigRoot() (string, error) {
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config"), nil
}

// userDataRoot returns ~/.local/share on non-Windows builds.
func userDataRoot() (string, error) {
	if v := os.Getenv("XDG_DATA_HOME"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share"), nil
}

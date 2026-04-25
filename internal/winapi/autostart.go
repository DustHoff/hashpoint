package winapi

// Autostart manages the user-scope auto-start registry entry under
// HKCU\Software\Microsoft\Windows\CurrentVersion\Run.
type Autostart interface {
	// Enabled reports whether the autostart entry is present.
	Enabled() (bool, error)
	// Enable installs/updates the autostart entry pointing at exePath.
	Enable(exePath string) error
	// Disable removes the autostart entry; missing entry is not an error.
	Disable() error
}

// NewAutostart returns a platform-specific implementation, or one that always
// returns ErrUnsupported on non-Windows builds.
func NewAutostart(appName string) Autostart {
	return newAutostart(appName)
}

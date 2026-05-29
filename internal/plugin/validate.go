package plugin

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ErrInvalidPluginName is returned by ValidatePluginName for a name that is
// not a single, safe path component.
var ErrInvalidPluginName = errors.New("plugin: invalid plugin name")

// ValidatePluginName rejects any name that is not a single safe path
// component, so a plugin-supplied name (e.g. from a management source
// plugin's catalog) cannot escape PluginsDir when it is joined into a
// filesystem path or used to launch a binary. It rejects the empty string,
// "." and "..", names containing a path separator, absolute paths, and
// volume/drive-prefixed names.
func ValidatePluginName(name string) error {
	switch {
	case name == "", name == ".", name == "..":
		return fmt.Errorf("%w: %q", ErrInvalidPluginName, name)
	case name != filepath.Base(name):
		return fmt.Errorf("%w: %q", ErrInvalidPluginName, name)
	case strings.ContainsAny(name, `/\`):
		return fmt.Errorf("%w: %q", ErrInvalidPluginName, name)
	case filepath.IsAbs(name), filepath.VolumeName(name) != "":
		return fmt.Errorf("%w: %q", ErrInvalidPluginName, name)
	}
	return nil
}

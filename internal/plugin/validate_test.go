package plugin

import (
	"errors"
	"testing"
)

func TestValidatePluginName(t *testing.T) {
	t.Parallel()

	valid := []string{"oncall-notify", "a", "plugin_1", "a.b-c", "Plugin42"}
	for _, name := range valid {
		if err := ValidatePluginName(name); err != nil {
			t.Errorf("ValidatePluginName(%q) = %v, want nil", name, err)
		}
	}

	invalid := []string{
		"",
		".",
		"..",
		"../x",
		`..\x`,
		"a/b",
		`a\b`,
		"/abs/path",
		`C:\x`,
		`\\server\share`,
	}
	for _, name := range invalid {
		if err := ValidatePluginName(name); !errors.Is(err, ErrInvalidPluginName) {
			t.Errorf("ValidatePluginName(%q) = %v, want ErrInvalidPluginName", name, err)
		}
	}
}

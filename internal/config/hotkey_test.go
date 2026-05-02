package config

import "testing"

func TestParseHotkey(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in       string
		wantMod  uint32
		wantVK   uint32
		wantCano string
		wantErr  bool
	}{
		{"Ctrl+Alt+T", HotkeyModCtrl | HotkeyModAlt, 'T', "Ctrl+Alt+T", false},
		{"ctrl+alt+t", HotkeyModCtrl | HotkeyModAlt, 'T', "Ctrl+Alt+T", false},
		{"Win+Y", HotkeyModWin, 'Y', "Win+Y", false},
		{"Shift+Ctrl+5", HotkeyModShift | HotkeyModCtrl, '5', "Shift+Ctrl+5", false},
		{"Ctrl+F12", HotkeyModCtrl, 0x70 + 11, "Ctrl+F12", false},
		{"Super+/", 0, 0, "", true},     // unsupported key
		{"T", 0, 0, "", true},           // no modifier
		{"", 0, 0, "", true},            // empty
		{"Ctrl+Ctrl+T", 0, 0, "", true}, // duplicate modifier
		{"Ctrl+Foo+T", 0, 0, "", true},  // unknown modifier
		{"Ctrl+", 0, 0, "", true},       // empty key
	}
	for _, tc := range cases {
		got, err := ParseHotkey(tc.in)
		if (err != nil) != tc.wantErr {
			t.Errorf("ParseHotkey(%q) err=%v, wantErr=%v", tc.in, err, tc.wantErr)
			continue
		}
		if tc.wantErr {
			continue
		}
		if got.Modifiers != tc.wantMod || got.VirtualKey != tc.wantVK || got.Canonical != tc.wantCano {
			t.Errorf("ParseHotkey(%q) = %+v, want mods=%d vk=%d cano=%q",
				tc.in, got, tc.wantMod, tc.wantVK, tc.wantCano)
		}
	}
}

func TestValidate_RejectsBadHotkey(t *testing.T) {
	t.Parallel()
	c := Default()
	c.QuickTag.Hotkey = "Ctrl+Foo"
	if err := c.Validate(); err == nil {
		t.Fatal("expected validation error for invalid hotkey")
	}
	c.QuickTag.Enabled = false
	if err := c.Validate(); err != nil {
		t.Fatalf("disabled hotkey should not be validated: %v", err)
	}
}

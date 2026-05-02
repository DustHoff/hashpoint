package config

import (
	"errors"
	"fmt"
	"strings"
)

// Hotkey modifier bitmask values. They mirror the Win32 MOD_* constants
// from winuser.h so the winapi layer can register the parsed value
// directly without a translation step.
const (
	HotkeyModAlt   = 0x0001
	HotkeyModCtrl  = 0x0002
	HotkeyModShift = 0x0004
	HotkeyModWin   = 0x0008
)

// ParsedHotkey is the result of parsing a hotkey string like "Ctrl+Alt+T".
// VirtualKey holds the Win32 VK_* code; Modifiers is an OR-combination of
// the HotkeyMod* bitmasks above. Canonical is the re-rendered string in
// the project's canonical case ("Ctrl+Alt+T") for round-trip persistence.
type ParsedHotkey struct {
	Modifiers  uint32
	VirtualKey uint32
	Canonical  string
}

// ParseHotkey parses a hotkey string of the form "<Mod>+<Mod>+...+<Key>".
// Recognised modifiers (case-insensitive): Ctrl, Control, Alt, Shift,
// Win, Super, Meta. Keys: A-Z, 0-9, F1-F24. At least one modifier is
// required — bare letters would clash with text input far too often.
func ParseHotkey(s string) (ParsedHotkey, error) {
	raw := strings.TrimSpace(s)
	if raw == "" {
		return ParsedHotkey{}, errors.New("hotkey is empty")
	}
	parts := strings.Split(raw, "+")
	if len(parts) < 2 {
		return ParsedHotkey{}, fmt.Errorf("hotkey %q needs at least one modifier and one key", raw)
	}
	var (
		mods  uint32
		key   uint32
		names []string
	)
	for i, p := range parts {
		token := strings.TrimSpace(p)
		if token == "" {
			return ParsedHotkey{}, fmt.Errorf("hotkey %q has an empty token", raw)
		}
		if i < len(parts)-1 {
			bit, name, err := modifierBit(token)
			if err != nil {
				return ParsedHotkey{}, err
			}
			if mods&bit != 0 {
				return ParsedHotkey{}, fmt.Errorf("hotkey %q lists modifier %s twice", raw, name)
			}
			mods |= bit
			names = append(names, name)
			continue
		}
		vk, kname, err := keyCode(token)
		if err != nil {
			return ParsedHotkey{}, err
		}
		key = vk
		names = append(names, kname)
	}
	if mods == 0 {
		return ParsedHotkey{}, fmt.Errorf("hotkey %q needs at least one modifier", raw)
	}
	if key == 0 {
		return ParsedHotkey{}, fmt.Errorf("hotkey %q has no key", raw)
	}
	return ParsedHotkey{Modifiers: mods, VirtualKey: key, Canonical: strings.Join(names, "+")}, nil
}

func modifierBit(token string) (uint32, string, error) {
	switch strings.ToLower(token) {
	case "ctrl", "control":
		return HotkeyModCtrl, "Ctrl", nil
	case "alt":
		return HotkeyModAlt, "Alt", nil
	case "shift":
		return HotkeyModShift, "Shift", nil
	case "win", "super", "meta":
		return HotkeyModWin, "Win", nil
	}
	return 0, "", fmt.Errorf("unknown modifier %q", token)
}

// keyCode returns the Win32 VK_* code for a printable key name. Only the
// subset relevant for a global hotkey (letters, digits, function keys)
// is supported — extending this list should be deliberate, not implicit.
func keyCode(token string) (uint32, string, error) {
	t := strings.ToUpper(strings.TrimSpace(token))
	if len(t) == 0 {
		return 0, "", errors.New("empty key")
	}
	if len(t) == 1 {
		c := t[0]
		switch {
		case c >= 'A' && c <= 'Z':
			return uint32(c), t, nil
		case c >= '0' && c <= '9':
			return uint32(c), t, nil
		}
	}
	if len(t) >= 2 && t[0] == 'F' {
		var n int
		if _, err := fmt.Sscanf(t, "F%d", &n); err == nil && n >= 1 && n <= 24 {
			// VK_F1 = 0x70, VK_F24 = 0x87.
			return uint32(0x70 + n - 1), fmt.Sprintf("F%d", n), nil
		}
	}
	return 0, "", fmt.Errorf("unsupported key %q (use A-Z, 0-9 or F1-F24)", token)
}

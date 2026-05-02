//go:build !windows

package winapi

import "log/slog"

// Rect is a screen-coordinate rectangle in physical pixels.
type Rect struct {
	Left, Top, Right, Bottom int32
}

// Width returns the rectangle's width in pixels.
func (r Rect) Width() int32 { return r.Right - r.Left }

// Height returns the rectangle's height in pixels.
func (r Rect) Height() int32 { return r.Bottom - r.Top }

// CursorMonitorWorkArea is unsupported on non-Windows platforms.
func CursorMonitorWorkArea() (Rect, error) { return Rect{}, ErrUnsupported }

// HotkeyManager is a no-op stub on non-Windows platforms so the rest of
// the program type-checks.
type HotkeyManager struct{}

// NewHotkeyManager returns a zero-cost stub.
func NewHotkeyManager(_ *slog.Logger) *HotkeyManager { return &HotkeyManager{} }

// Start is a no-op.
func (*HotkeyManager) Start() error { return nil }

// SetHotkey is a no-op.
func (*HotkeyManager) SetHotkey(_ bool, _, _ uint32, _ func()) error { return ErrUnsupported }

// Stop is a no-op.
func (*HotkeyManager) Stop() {}

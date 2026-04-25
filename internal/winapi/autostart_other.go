//go:build !windows

package winapi

type stubAutostart struct{}

func newAutostart(_ string) Autostart {
	return stubAutostart{}
}

func (stubAutostart) Enabled() (bool, error)   { return false, ErrUnsupported }
func (stubAutostart) Enable(_ string) error    { return ErrUnsupported }
func (stubAutostart) Disable() error           { return ErrUnsupported }

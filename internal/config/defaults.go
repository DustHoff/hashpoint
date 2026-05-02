package config

// Default returns a config with sensible defaults applied.
func Default() *Config {
	return &Config{
		Tracking: TrackingConfig{
			PollIntervalSec:  2,
			IdleThresholdMin: 5,
			Enabled:          true,
		},
		Personio: PersonioConfig{
			Tenant: "",
		},
		UI: UIConfig{
			Autostart: true,
		},
		QuickTag: QuickTagConfig{
			Enabled: true,
			Hotkey:  "Ctrl+Alt+T",
		},
	}
}

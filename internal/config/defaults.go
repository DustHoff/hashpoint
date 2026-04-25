package config

// Default returns a config with sensible defaults applied.
func Default() *Config {
	return &Config{
		Tracking: TrackingConfig{
			PollIntervalSec:  2,
			IdleThresholdMin: 5,
		},
		Personio: PersonioConfig{
			Tenant: "",
		},
		UI: UIConfig{
			Autostart: true,
		},
	}
}

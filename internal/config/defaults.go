package config

// Default returns a config with sensible defaults applied.
func Default() *Config {
	return &Config{
		Tracking: TrackingConfig{
			PollIntervalSec:  2,
			IdleThresholdMin: 5,
		},
		Personio: PersonioConfig{
			ClientID:   "",
			EmployeeID: "",
			BaseURL:    "https://api.personio.de/v1",
		},
		UI: UIConfig{
			Autostart: true,
		},
	}
}

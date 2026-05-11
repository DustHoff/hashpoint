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
		Entra: EntraConfig{
			ClientID: "",
			TenantID: "",
		},
		QuickTag: QuickTagConfig{
			Enabled: true,
			Hotkey:  "Ctrl+Alt+T",
		},
		Communication: CommunicationConfig{
			ProcessNames:        []string{"teams.exe"},
			TitleExcludePhrases: nil,
		},
		WorkSchedule: WorkScheduleConfig{
			StartHour: 8,
			EndHour:   18,
			WorkDays:  []string{"Mon", "Tue", "Wed", "Thu", "Fri"},
		},
		OnCall: OnCallConfig{
			TagIDs: nil,
		},
		Plugins: map[string]map[string]string{},
	}
}

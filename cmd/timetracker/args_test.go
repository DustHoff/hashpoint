package main

import "testing"

func TestDispatchArgs(t *testing.T) {
	tests := []struct {
		name             string
		args             []string
		wantMode         procMode
		wantRelaunchFlag bool
	}{
		{"no flags is monolith", nil, modeMonolith, false},
		{"user-launched collector", []string{"--collector"}, modeCollector, false},
		{"watchdog-launched collector", []string{"--collector", watchdogRelaunchFlag}, modeCollector, true},
		{"flag order irrelevant", []string{watchdogRelaunchFlag, "--collector"}, modeCollector, true},
		{"prefix is not the flag", []string{"--collector", watchdogRelaunchFlag + "=1"}, modeCollector, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if mode, _ := parseArgs(tt.args); mode != tt.wantMode {
				t.Errorf("parseArgs mode = %v, want %v", mode, tt.wantMode)
			}
			if got := hasArg(tt.args, watchdogRelaunchFlag); got != tt.wantRelaunchFlag {
				t.Errorf("hasArg(%q) = %v, want %v", watchdogRelaunchFlag, got, tt.wantRelaunchFlag)
			}
		})
	}
}

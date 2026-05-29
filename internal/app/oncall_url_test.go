package app

import "testing"

// TestSafeExternalURL verifies the server-side scheme allowlist: only
// absolute http(s) URLs survive, so a malicious plugin cannot smuggle a
// javascript:/data:/file: URI into the submission link rendered by the UI.
func TestSafeExternalURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"https passes", "https://example.com/x?y=1", "https://example.com/x?y=1"},
		{"http passes", "http://example.com", "http://example.com"},
		{"uppercase scheme passes", "HTTPS://example.com", "HTTPS://example.com"},
		{"javascript rejected", "javascript:window.go.app.App.PluginGetConfig('x')", ""},
		{"data rejected", "data:text/html,<script>alert(1)</script>", ""},
		{"vbscript rejected", "vbscript:msgbox(1)", ""},
		{"file rejected", "file:///C:/Windows/System32", ""},
		{"relative rejected", "/relative/path", ""},
		{"bare word rejected", "notaurl", ""},
		{"empty rejected", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := safeExternalURL(tt.in); got != tt.want {
				t.Errorf("safeExternalURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

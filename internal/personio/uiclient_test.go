package personio

import (
	"strings"
	"testing"
)

// TestDecodeLimitedN verifies that decoding refuses oversized payloads
// instead of buffering them — a hostile/MITM'd endpoint must not be able to
// drive unbounded memory use through a Personio response.
func TestDecodeLimitedN(t *testing.T) {
	t.Parallel()
	type doc struct {
		A string `json:"a"`
	}
	tests := []struct {
		name    string
		body    string
		max     int64
		wantErr bool
		wantA   string
	}{
		{name: "within limit", body: `{"a":"hi"}`, max: 64, wantA: "hi"},
		{name: "exactly fits", body: `{"a":"hello"}`, max: int64(len(`{"a":"hello"}`)), wantA: "hello"},
		{name: "oversized valid value errors", body: `{"a":"` + strings.Repeat("x", 200) + `"}`, max: 32, wantErr: true},
		{name: "huge trailing value errors", body: `{"a":"` + strings.Repeat("y", 10000) + `"}`, max: 128, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got doc
			err := decodeLimitedN(strings.NewReader(tt.body), &got, tt.max)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for oversized body, got nil (decoded %q)", got.A)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.A != tt.wantA {
				t.Fatalf("got A=%q, want %q", got.A, tt.wantA)
			}
		})
	}
}

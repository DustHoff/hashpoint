package tagging

import (
	"errors"
	"testing"
)

func TestNormalizeName(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"projekta", "#projekta", false},
		{"#projekta", "#projekta", false},
		{"  #abc123  ", "#abc123", false},
		{"#PROJEKT", "#PROJEKT", false},
		{"#with space", "", true},
		{"#with-dash", "", true},
		{"", "", true},
		{"#", "", true},
		{"#ä", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got, err := NormalizeName(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				if !errors.Is(err, ErrInvalidTagName) {
					t.Fatalf("expected ErrInvalidTagName, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

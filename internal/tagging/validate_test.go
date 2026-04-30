package tagging

import (
	"errors"
	"strings"
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

func TestNormalizeRuleDescription(t *testing.T) {
	t.Run("nil stays nil", func(t *testing.T) {
		got, err := NormalizeRuleDescription(nil)
		if err != nil || got != nil {
			t.Fatalf("got (%v, %v), want (nil, nil)", got, err)
		}
	})
	t.Run("whitespace becomes nil", func(t *testing.T) {
		s := "   "
		got, err := NormalizeRuleDescription(&s)
		if err != nil || got != nil {
			t.Fatalf("got (%v, %v), want (nil, nil)", got, err)
		}
	})
	t.Run("trims and keeps", func(t *testing.T) {
		s := "  Recherche  "
		got, err := NormalizeRuleDescription(&s)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got == nil || *got != "Recherche" {
			t.Fatalf("got %v, want pointer to %q", got, "Recherche")
		}
	})
	t.Run("rejects too long", func(t *testing.T) {
		s := strings.Repeat("a", MaxRuleDescriptionLength+1)
		_, err := NormalizeRuleDescription(&s)
		if !errors.Is(err, ErrRuleDescriptionTooLong) {
			t.Fatalf("expected ErrRuleDescriptionTooLong, got %v", err)
		}
	})
	t.Run("counts runes not bytes", func(t *testing.T) {
		// 250 multi-byte runes = far more than 250 bytes — must still pass.
		s := strings.Repeat("ä", MaxRuleDescriptionLength)
		got, err := NormalizeRuleDescription(&s)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got == nil || *got != s {
			t.Fatalf("expected unchanged string, got %v", got)
		}
	})
}

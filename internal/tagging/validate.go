// Package tagging provides hashtag validation, tag-hierarchy resolution and
// the auto-tagging rules engine.
//
// Names follow ^#[A-Za-z0-9]+$ — a leading "#" plus one or more alphanumerics.
// Sub-tags inherit Personio mappings from their parent unless they override.
package tagging

import (
	"errors"
	"regexp"
	"strings"
)

// nameRegex matches a valid hashtag name.
var nameRegex = regexp.MustCompile(`^#[A-Za-z0-9]+$`)

// ErrInvalidTagName is returned when a name does not match the hashtag schema.
var ErrInvalidTagName = errors.New("tag name must match ^#[A-Za-z0-9]+$")

// NormalizeName trims whitespace and ensures the leading "#"; the resulting
// name is then validated against the hashtag schema.
func NormalizeName(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", ErrInvalidTagName
	}
	if !strings.HasPrefix(s, "#") {
		s = "#" + s
	}
	if !nameRegex.MatchString(s) {
		return "", ErrInvalidTagName
	}
	return s, nil
}

// IsValidName reports whether the given (already-normalized) name is valid.
func IsValidName(s string) bool { return nameRegex.MatchString(s) }

// MaxRuleDescriptionLength caps the optional rule description so it stays
// reasonable for inline display and the Personio comment.
const MaxRuleDescriptionLength = 250

// ErrRuleDescriptionTooLong is returned when the rule description exceeds
// MaxRuleDescriptionLength runes.
var ErrRuleDescriptionTooLong = errors.New("rule description exceeds 250 characters")

// NormalizeRuleDescription trims surrounding whitespace and validates the
// rule description. Returns nil for empty/whitespace-only input (treated as
// "no description") and an error if the trimmed value exceeds 250 runes.
func NormalizeRuleDescription(raw *string) (*string, error) {
	if raw == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*raw)
	if trimmed == "" {
		return nil, nil
	}
	if utf8RuneCount(trimmed) > MaxRuleDescriptionLength {
		return nil, ErrRuleDescriptionTooLong
	}
	return &trimmed, nil
}

func utf8RuneCount(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}

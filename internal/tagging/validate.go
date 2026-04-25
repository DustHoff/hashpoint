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

package tagging

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/dusthoff/hashpoint/internal/storage"
)

// CompiledRule is a rule with a precompiled regex (for MatchRegex) ready for
// fast evaluation against many blocks.
type CompiledRule struct {
	Rule storage.Rule
	re   *regexp.Regexp // nil unless MatchRegex
}

// Compile converts a slice of rules into compiled rules.
// Rules with invalid regexes are rejected — callers should validate at write
// time, but the engine also re-checks here defensively.
func Compile(rules []storage.Rule) ([]CompiledRule, error) {
	out := make([]CompiledRule, 0, len(rules))
	for _, r := range rules {
		cr := CompiledRule{Rule: r}
		if r.MatchType == storage.MatchRegex {
			re, err := regexp.Compile(r.Pattern)
			if err != nil {
				return nil, fmt.Errorf("rule %d: invalid regex %q: %w", r.ID, r.Pattern, err)
			}
			cr.re = re
		}
		out = append(out, cr)
	}
	return out, nil
}

// ValidatePattern checks that a regex pattern is RE2-compilable. For
// non-regex types it just verifies the pattern is non-empty.
func ValidatePattern(matchType storage.MatchType, pattern string) error {
	if pattern == "" {
		return fmt.Errorf("pattern must not be empty")
	}
	if matchType == storage.MatchRegex {
		if _, err := regexp.Compile(pattern); err != nil {
			return fmt.Errorf("invalid regex: %w", err)
		}
	}
	return nil
}

// Match reports whether the rule matches the given block.
func (cr CompiledRule) Match(processName, windowTitle string) bool {
	switch cr.Rule.MatchField {
	case storage.MatchProcessName:
		return matchValue(cr, processName)
	case storage.MatchWindowTitle:
		return matchValue(cr, windowTitle)
	case storage.MatchBoth:
		return matchValue(cr, processName) && matchValue(cr, windowTitle)
	default:
		return false
	}
}

func matchValue(cr CompiledRule, v string) bool {
	switch cr.Rule.MatchType {
	case storage.MatchContains:
		return strings.Contains(strings.ToLower(v), strings.ToLower(cr.Rule.Pattern))
	case storage.MatchEquals:
		return strings.EqualFold(v, cr.Rule.Pattern)
	case storage.MatchRegex:
		return cr.re != nil && cr.re.MatchString(v)
	default:
		return false
	}
}

// FirstMatch returns the first rule (by sort order — caller's responsibility
// to pre-sort by priority DESC) whose match conditions are satisfied.
func FirstMatch(rules []CompiledRule, processName, windowTitle string) *CompiledRule {
	for i := range rules {
		if rules[i].Match(processName, windowTitle) {
			return &rules[i]
		}
	}
	return nil
}

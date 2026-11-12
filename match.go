package netsec

import (
	"strings"
)

// wildcardOnlyInLabel reports whether pattern is a valid glob pattern whose
// '*' wildcards are confined to a single label: a wildcard never matches the
// label separator '.', so patterns such as "*.example.com" or "a*.b" are
// allowed while patterns with empty labels (e.g. "a..b") are rejected.
func wildcardOnlyInLabel(pattern string) bool {
	if pattern == "" {
		return false
	}
	labels := strings.Split(pattern, ".")
	for _, label := range labels {
		if label == "" {
			return false
		}
	}
	return true
}

// patternEntry is a pre-validated matchPattern split into its labels.
type patternEntry struct {
	raw    string
	labels []string
}

func newPatternEntry(pattern string) (patternEntry, bool) {
	if !wildcardOnlyInLabel(pattern) {
		return patternEntry{}, false
	}
	return patternEntry{raw: pattern, labels: strings.Split(pattern, ".")}, true
}

// matchLabel reports whether a single name label matches a pattern label.
// Within a label '*' matches any run of characters (including the empty run);
// every other character matches itself. Labels never contain '.', so a
// wildcard can never cross a label boundary.
func matchLabel(pattern, label string) bool {
	pi, li := 0, 0
	starP, starL := -1, 0
	for li < len(label) {
		switch {
		case pi < len(pattern) && pattern[pi] == label[li]:
			pi++
			li++
		case pi < len(pattern) && pattern[pi] == '*':
			starP = pi
			starL = li
			pi++
		case starP != -1:
			pi = starP + 1
			starL++
			li = starL
		default:
			return false
		}
	}
	for pi < len(pattern) && pattern[pi] == '*' {
		pi++
	}
	return pi == len(pattern)
}

// match reports whether a normalized query name matches this pattern: the
// label counts must be identical and every label must match individually,
// which guarantees '*' never spans a dot.
func (p patternEntry) match(nameLabels []string) bool {
	if len(p.labels) != len(nameLabels) {
		return false
	}
	for i, label := range nameLabels {
		if !matchLabel(p.labels[i], label) {
			return false
		}
	}
	return true
}

// normalizeName lower-cases a DNS name and strips a single trailing dot so
// that "Example.COM." and "example.com" are treated as the same name.
func normalizeName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	return strings.TrimSuffix(name, ".")
}

// nameLabels splits a normalized name into its labels.
func nameLabels(name string) []string {
	if name == "" {
		return nil
	}
	return strings.Split(name, ".")
}

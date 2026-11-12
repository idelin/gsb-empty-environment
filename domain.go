package netsec

import "strings"

// normalizeDomain lower-cases a DNS name and strips a single trailing dot
// (the root label).
func normalizeDomain(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	return strings.TrimSuffix(name, ".")
}

// isValidDomain reports whether name is a syntactically valid DNS name:
// non-empty dot-separated labels made of letters, digits and hyphens, with
// no leading/trailing hyphen, and total length within 253 characters.
func isValidDomain(name string) bool {
	name = normalizeDomain(name)
	if name == "" || len(name) > 253 {
		return false
	}
	labels := strings.Split(name, ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 {
			return false
		}
		for i, r := range label {
			switch {
			case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			case r == '-':
				if i == 0 || i == len(label)-1 {
					return false
				}
			default:
				return false
			}
		}
	}
	return true
}

// validateWildcard enforces that a wildcard DNS name may only use a bare "*"
// as its leftmost label and must have at least one further label. Names
// without a wildcard are checked as ordinary domains.
func validateWildcard(name string) bool {
	name = normalizeDomain(name)
	labels := strings.Split(name, ".")
	for i, label := range labels {
		if !strings.Contains(label, "*") {
			continue
		}
		// Wildcard is legal only when it is the whole leftmost label.
		if i != 0 || label != "*" || len(labels) < 2 {
			return false
		}
	}
	return isValidDomain(strings.Replace(name, "*.", "wildcard.", 1))
}

// hasLabelSuffix reports whether name equals suffix or ends with "."+suffix.
// The label-boundary comparison guarantees that "notexample.com" does not
// match the suffix "example.com".
func hasLabelSuffix(name, suffix string) bool {
	name = normalizeDomain(name)
	suffix = normalizeDomain(suffix)
	if suffix == "" {
		return false
	}
	if name == suffix {
		return true
	}
	return strings.HasSuffix(name, "."+suffix)
}

// matchesSuffixSet reports whether name matches any of the allowed suffixes.
// A wildcard name ("*.suffix") is accepted when the portion after "*."
// itself sits within an allowed suffix.
func matchesSuffixSet(name string, suffixes []string) bool {
	base := normalizeDomain(name)
	if wildcard, ok := strings.CutPrefix(base, "*."); ok {
		base = wildcard
	}
	for _, suffix := range suffixes {
		if hasLabelSuffix(base, suffix) {
			return true
		}
	}
	return false
}

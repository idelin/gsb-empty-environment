package workloadid

import (
	"errors"
	"strings"
)

// Wildcard policy errors. They are returned wrapped so callers can use errors.Is.
var (
	// ErrWildcardNotLeftmost means the wildcard label is not the full left-most
	// label (for example "evil.*.prod" or "ev*.prod").
	ErrWildcardNotLeftmost = errors.New("workloadid: wildcard must be the complete left-most label")
	// ErrMultiLabelWildcard means the pattern would match more than one DNS
	// label (for example "*.prod" matching "a.b.prod" is rejected at match time;
	// patterns such as "*" are rejected up front).
	ErrMultiLabelWildcard = errors.New("workloadid: wildcard cannot match multiple labels")
	// ErrIDNWildcard means a punycode label is combined with a wildcard.
	ErrIDNWildcard = errors.New("workloadid: wildcard must not be combined with IDN punycode")
	// ErrEmptyDNSName means an empty name was presented.
	ErrEmptyDNSName = errors.New("workloadid: empty DNS name")
)

// punycodePrefix is the ACE label prefix defined by RFC 3490.
const punycodePrefix = "xn--"

// DNSMatcher applies an explicitly declared wildcard policy to DNS SAN values.
//
// Security properties:
//   - A wildcard is accepted only as the entire left-most label ("*.example.com").
//   - "*.example.com" matches exactly one additional label ("a.example.com") and
//     never multiple labels ("a.b.example.com" is rejected).
//   - Wildcards and IDN punycode labels ("xn--...") never coexist in either the
//     policy pattern or the presented name.
//   - Non-wildcard patterns require an exact, case-insensitive match.
//
// The matcher never derives identity from a bare suffix: the pattern "*.prod"
// only matches single-label hosts under prod and cannot be used to equate
// "evil.prod" with a production workload unless the policy explicitly declares
// the pattern and the SPIFFE/attestation checks also pass.
type DNSMatcher struct {
	// AllowedPatterns is the explicit allow list. A wildcard must be the full
	// left-most label.
	AllowedPatterns []string
	// MaxLabels bounds the label depth of presented names and patterns. Zero
	// means a conservative default is applied.
	MaxLabels int
}

const defaultMaxLabels = 12

// ValidatePattern validates one declared pattern at configuration load time.
func ValidatePattern(pattern string) error {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	if pattern == "" {
		return ErrEmptyDNSName
	}
	if strings.ContainsAny(pattern, " \t\r\n/") {
		return ErrWildcardNotLeftmost
	}
	labels := strings.Split(pattern, ".")
	if containsEmptyLabel(labels) {
		return ErrWildcardNotLeftmost
	}
	starCount := 0
	for i, label := range labels {
		if strings.Contains(label, "*") {
			if i != 0 || label != "*" {
				return ErrWildcardNotLeftmost
			}
			starCount++
		}
		if strings.HasPrefix(label, punycodePrefix) && starCount > 0 {
			return ErrIDNWildcard
		}
	}
	if starCount > 1 {
		return ErrWildcardNotLeftmost
	}
	// A lone "*" or "*.<tld>" that could span an unbounded space is rejected:
	// after the wildcard there must be at least two anchor labels.
	if starCount == 1 && len(labels[1:]) < 2 {
		return ErrMultiLabelWildcard
	}
	return nil
}

func containsEmptyLabel(labels []string) bool {
	for _, label := range labels {
		if label == "" {
			return true
		}
	}
	return false
}

// Match reports whether name is accepted by the explicit pattern list.
func (m DNSMatcher) Match(name string) (bool, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return false, ErrEmptyDNSName
	}
	maxLabels := m.MaxLabels
	if maxLabels <= 0 {
		maxLabels = defaultMaxLabels
	}
	nameLabels := strings.Split(name, ".")
	if containsEmptyLabel(nameLabels) || len(nameLabels) > maxLabels {
		return false, ErrMultiLabelWildcard
	}

	for _, pattern := range m.AllowedPatterns {
		pattern = strings.ToLower(strings.TrimSpace(pattern))
		if err := ValidatePattern(pattern); err != nil {
			return false, err
		}
		patternLabels := strings.Split(pattern, ".")
		if len(patternLabels) > maxLabels {
			continue
		}

		if patternLabels[0] == "*" {
			// Wildcard path: reject punycode on either side.
			if hasPunycode(nameLabels) || hasPunycode(patternLabels) {
				return false, ErrIDNWildcard
			}
			// The wildcard stands for exactly ONE label (RFC 6125 style): the
			// pattern label "*" is replaced by a single presented label, so the
			// name and pattern have the same label count and every anchor label
			// (everything after the wildcard) must be equal. A deeper name such
			// as "a.b.example.com" against "*.example.com" has too many labels
			// and is rejected. This is the multi-level-subdomain guard.
			if len(nameLabels) != len(patternLabels) ||
				!equalLabels(nameLabels[1:], patternLabels[1:]) {
				continue
			}
			return true, nil
		}

		// Exact match path. A presented punycode name may only match an exact
		// punycode pattern; wildcards are not involved.
		if len(nameLabels) != len(patternLabels) {
			continue
		}
		if equalLabels(nameLabels, patternLabels) {
			return true, nil
		}
	}
	return false, nil
}

func hasPunycode(labels []string) bool {
	for _, label := range labels {
		if strings.HasPrefix(label, punycodePrefix) {
			return true
		}
	}
	return false
}

func equalLabels(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

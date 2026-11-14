package netsec

import (
	"fmt"
	"net/netip"
	"strings"
)

// SANUse describes what a SAN is allowed to prove. DNS names are only ever
// hostnames; they are never identities or addresses.
type SANUse uint8

const (
	SANUseHostname SANUse = 1
	SANUseIdentity SANUse = 2
	SANUseAddress  SANUse = 3
)

// DNSNameRule is an explicit DNS policy declaration. An exact rule contains a
// literal DNS name. A wildcard rule contains one '*' as its entire leftmost
// label, matches exactly one label, and may only be used as a hostname.
//
// Depth 0 is the default and is equivalent to a depth of 1. A depth greater
// than 1 is rejected: multi-label wildcard expansion is never permitted.
type DNSNameRule struct {
	Pattern string
	Use     SANUse
	Depth   int
}

// ExactDNSName constructs an exact, hostname-only DNS rule.
func ExactDNSName(name string) DNSNameRule {
	return DNSNameRule{Pattern: name, Use: SANUseHostname}
}

// WildcardDNSName constructs a single-label, hostname-only wildcard rule.
func WildcardDNSName(pattern string) DNSNameRule {
	return DNSNameRule{Pattern: pattern, Use: SANUseHostname, Depth: 1}
}

// Validate checks the rule's syntax, wildcard depth, and principal use.
func (r DNSNameRule) Validate() error {
	_, wildcard, err := canonicalDNSPattern(r.Pattern)
	if err != nil {
		return err
	}

	use := r.Use
	if use == 0 {
		use = SANUseHostname
	}
	if use != SANUseHostname {
		if wildcard {
			return fmt.Errorf("%w: DNS wildcard may only be used for hostnames", ErrWildcardPrincipal)
		}
		return fmt.Errorf("%w: DNS SAN may not be used as an identity or address", ErrSANTypeSubstitution)
	}

	if !wildcard {
		if r.Depth != 0 {
			return fmt.Errorf("%w: depth is only meaningful for wildcard rules", ErrWildcardNotAllowed)
		}
		return nil
	}

	if r.Depth < 0 || r.Depth > 1 {
		return fmt.Errorf("%w: wildcard depth %d exceeds 1", ErrMultiLevelWildcard, r.Depth)
	}
	return nil
}

// Match reports whether a literal DNS name matches this rule. A wildcard rule
// replaces exactly one complete label; it does not match a multi-label suffix.
// Match never establishes a SPIFFE identity.
func (r DNSNameRule) Match(name string) bool {
	canonicalName, err := canonicalDNSName(name)
	if err != nil {
		return false
	}
	pattern, wildcard, err := canonicalDNSPattern(r.Pattern)
	if err != nil {
		return false
	}
	if !wildcard {
		return canonicalName == pattern
	}

	nameLabels := strings.Split(canonicalName, ".")
	patternLabels := strings.Split(pattern, ".")
	if len(nameLabels) != len(patternLabels) {
		return false
	}
	if nameLabels[0] == "" {
		return false
	}
	for i := 1; i < len(patternLabels); i++ {
		if nameLabels[i] != patternLabels[i] {
			return false
		}
	}
	return true
}

// ValidateWildcardPattern validates an explicitly declared wildcard pattern
// without constructing a rule.
func ValidateWildcardPattern(pattern string) error {
	_, wildcard, err := canonicalDNSPattern(pattern)
	if err != nil {
		return err
	}
	if !wildcard {
		return fmt.Errorf("%w: pattern has no wildcard", ErrWildcardNotAllowed)
	}
	return nil
}

func canonicalDNSPattern(pattern string) (string, bool, error) {
	if pattern == "" {
		return "", false, fmt.Errorf("%w: empty DNS pattern", ErrWildcardNotAllowed)
	}
	if !strings.Contains(pattern, "*") {
		name, err := canonicalDNSName(pattern)
		return name, false, err
	}

	lower := strings.ToLower(pattern)
	if len(lower) > 253 {
		return "", false, fmt.Errorf("%w: DNS pattern too long", ErrWildcardNotAllowed)
	}
	if strings.HasSuffix(lower, ".") {
		return "", false, fmt.Errorf("%w: DNS pattern must not end with a dot", ErrWildcardNotAllowed)
	}
	labels := strings.Split(lower, ".")
	if len(labels) < 2 {
		return "", false, fmt.Errorf("%w: wildcard needs a literal suffix", ErrWildcardNotAllowed)
	}
	if labels[0] != "*" {
		if strings.Contains(labels[0], "*") {
			return "", false, fmt.Errorf("%w: wildcard must be the whole leftmost label", ErrWildcardNotAllowed)
		}
		return "", false, fmt.Errorf("%w: wildcard must be the leftmost label", ErrWildcardNotAllowed)
	}
	for _, label := range labels[1:] {
		if label == "*" || strings.Contains(label, "*") {
			return "", false, fmt.Errorf("%w: wildcard appears outside the leftmost label", ErrMultiLevelWildcard)
		}
	}
	suffix, err := canonicalDNSName(strings.Join(labels[1:], "."))
	if err != nil {
		return "", false, err
	}
	return "*." + suffix, true, nil
}

func canonicalDNSName(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("%w: empty DNS name", ErrInvalidSAN)
	}
	if len(name) > 253 {
		return "", fmt.Errorf("%w: DNS name too long", ErrInvalidSAN)
	}
	if strings.HasSuffix(name, ".") {
		return "", fmt.Errorf("%w: DNS name must not end with a dot", ErrInvalidSAN)
	}
	name = strings.ToLower(name)
	if strings.Contains(name, "*") {
		return "", fmt.Errorf("%w: wildcard DNS certificate SAN is not accepted", ErrWildcardNotAllowed)
	}
	if _, err := netip.ParseAddr(name); err == nil {
		return "", fmt.Errorf("%w: IP literal encoded as a DNS name", ErrSANTypeSubstitution)
	}
	for _, label := range strings.Split(name, ".") {
		if !validDNSLabel(label) {
			return "", fmt.Errorf("%w: invalid DNS label %q", ErrInvalidSAN, label)
		}
	}
	return name, nil
}

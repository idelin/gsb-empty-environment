package nameconstraint

import (
	"net"
	"strings"
)

// normalizeDomain strips a single trailing dot (absolute-name marker) and
// lowercases the name. Both SANs and constraints are normalized before
// comparison so that "example.com." and "example.com" compare equal.
func normalizeDomain(s string) string {
	return strings.ToLower(strings.TrimSuffix(s, "."))
}

// labels splits a domain into lowercase labels after trailing-dot removal.
// It returns ok=false for names containing empty interior labels.
func labels(domain string) (parts []string, ok bool) {
	domain = normalizeDomain(domain)
	if domain == "" {
		return nil, false
	}
	for _, label := range strings.Split(domain, ".") {
		if label == "" {
			return nil, false
		}
		parts = append(parts, label)
	}
	return parts, true
}

// domainMatches reports whether name satisfies a single dNSName constraint.
// A leading-dot constraint (".example.com") only matches subdomains, while a
// bare constraint ("example.com") also matches the exact name. An empty
// constraint matches every DNS name.
func domainMatches(name, constraint string) (bool, error) {
	if constraint == "" {
		return true, nil
	}

	mustHaveSubdomain := false
	if strings.HasPrefix(constraint, ".") {
		mustHaveSubdomain = true
		constraint = constraint[1:]
	}

	nameLabels, ok := labels(name)
	if !ok {
		return false, &ConstraintError{Reason: ReasonMalformedName, NameType: "DNS",
			Name: name, Detail: "cannot parse DNS name " + name}
	}
	constraintLabels, ok := labels(constraint)
	if !ok {
		return false, &ConstraintError{Reason: ReasonMalformedConstraint, NameType: "DNS",
			Name: constraint, Detail: "cannot parse DNS constraint " + constraint}
	}

	if len(nameLabels) < len(constraintLabels) ||
		(mustHaveSubdomain && len(nameLabels) == len(constraintLabels)) {
		return false, nil
	}

	offset := len(nameLabels) - len(constraintLabels)
	for i, want := range constraintLabels {
		if nameLabels[offset+i] != want {
			return false, nil
		}
	}
	return true, nil
}

// wildcardBase strips a leftmost "*" label from a DNS SAN and returns the
// remaining domain. Only a leftmost label of exactly "*" is treated as a
// wildcard (RFC 6125); other wildcard placements are matched literally.
func wildcardBase(dns string) (base string, wildcard bool, err error) {
	normalized := normalizeDomain(dns)
	if normalized == "" {
		return "", false, &ConstraintError{Reason: ReasonMalformedName, NameType: "DNS",
			Name: dns, Detail: "cannot parse DNS name " + dns}
	}
	dot := strings.IndexByte(normalized, '.')
	if dot > 0 && normalized[:dot] == "*" {
		return normalized[dot+1:], true, nil
	}
	return normalized, false, nil
}

// dnsNameMatches evaluates one DNS SAN against one constraint. For wildcard
// SANs the "*" label is removed first and the remainder is matched normally;
// the removed label counts as the extra label required by a leading-dot
// constraint, so "*.example.com" satisfies both "example.com" and
// ".example.com".
func dnsNameMatches(dns, constraint string) (bool, error) {
	base, wildcard, err := wildcardBase(dns)
	if err != nil {
		return false, err
	}
	c := constraint
	if wildcard && strings.HasPrefix(c, ".") {
		// The wildcard label itself fulfills the "one extra label" rule,
		// so evaluate against the bare form of the constraint.
		c = c[1:]
	}
	return domainMatches(base, c)
}

func matchDNS(dns string, nc *nameConstraints) error {
	// Excluded subtrees are evaluated first and always win.
	for _, constraint := range nc.excludedDNS {
		match, err := dnsNameMatches(dns, constraint)
		if err != nil {
			return err
		}
		if match {
			return &ConstraintError{Reason: ReasonExcluded, NameType: "DNS", Name: dns,
				Detail: "DNS name " + dns + " matches excluded subtree " + constraint}
		}
	}

	if !nc.permittedDNSSet {
		return nil
	}
	for _, constraint := range nc.permittedDNS {
		match, err := dnsNameMatches(dns, constraint)
		if err != nil {
			return err
		}
		if match {
			return nil
		}
	}
	return &ConstraintError{Reason: ReasonNotPermitted, NameType: "DNS", Name: dns,
		Detail: "DNS name " + dns + " is not within any permitted subtree"}
}

// ipMatches checks one IP SAN against one CIDR constraint. Length
// mismatches (IPv4 vs IPv6) never match.
func ipMatches(ip net.IP, block *net.IPNet) bool {
	if len(ip) != len(block.IP) {
		return false
	}
	for i := range ip {
		if ip[i]&block.Mask[i] != block.IP[i]&block.Mask[i] {
			return false
		}
	}
	return true
}

func matchIP(ip net.IP, nc *nameConstraints) error {
	for _, block := range nc.excludedIP {
		if ipMatches(ip, block) {
			return &ConstraintError{Reason: ReasonExcluded, NameType: "IP", Name: ip.String(),
				Detail: "IP address " + ip.String() + " matches an excluded subtree"}
		}
	}

	if !nc.permittedIPSet {
		return nil
	}
	for _, block := range nc.permittedIP {
		if ipMatches(ip, block) {
			return nil
		}
	}
	return &ConstraintError{Reason: ReasonNotPermitted, NameType: "IP", Name: ip.String(),
		Detail: "IP address " + ip.String() + " is not within any permitted subtree"}
}

// emailParts splits an rfc822Name SAN into a local part and a lowercased
// domain. Local parts are compared case-sensitively per RFC 5280.
func emailParts(email string) (local, domain string, ok bool) {
	at := strings.LastIndexByte(email, '@')
	if at <= 0 || at == len(email)-1 {
		return "", "", false
	}
	return email[:at], normalizeDomain(email[at+1:]), true
}

func emailMatches(email, constraint string) (bool, error) {
	if strings.Contains(constraint, "@") {
		cl, cd, ok := emailParts(constraint)
		if !ok {
			return false, &ConstraintError{Reason: ReasonMalformedConstraint, NameType: "email",
				Name: constraint, Detail: "cannot parse email constraint " + constraint}
		}
		el, ed, ok := emailParts(email)
		if !ok {
			return false, &ConstraintError{Reason: ReasonMalformedName, NameType: "email",
				Name: email, Detail: "cannot parse email address " + email}
		}
		return el == cl && ed == cd, nil
	}

	_, domain, ok := emailParts(email)
	if !ok {
		return false, &ConstraintError{Reason: ReasonMalformedName, NameType: "email",
			Name: email, Detail: "cannot parse email address " + email}
	}
	// A domain-only email constraint is matched like a DNS constraint on the
	// domain part; a leading dot limits it to subdomains.
	return domainMatches(domain, constraint)
}

func matchEmail(email string, nc *nameConstraints) error {
	for _, constraint := range nc.excludedEmail {
		match, err := emailMatches(email, constraint)
		if err != nil {
			return err
		}
		if match {
			return &ConstraintError{Reason: ReasonExcluded, NameType: "email", Name: email,
				Detail: "email " + email + " matches excluded subtree " + constraint}
		}
	}

	if !nc.permittedMail {
		return nil
	}
	for _, constraint := range nc.permittedEmail {
		match, err := emailMatches(email, constraint)
		if err != nil {
			return err
		}
		if match {
			return nil
		}
	}
	return &ConstraintError{Reason: ReasonNotPermitted, NameType: "email", Name: email,
		Detail: "email " + email + " is not within any permitted subtree"}
}

package netsec

import (
	"crypto/x509"
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrDNSNameNotPermitted is returned when a name matches no permitted
	// DNS constraint.
	ErrDNSNameNotPermitted = errors.New("netsec: DNS name is not permitted")

	// ErrDNSNameExcluded is returned when a name matches an excluded DNS
	// constraint.
	ErrDNSNameExcluded = errors.New("netsec: DNS name is excluded")
)

// DNSNameConstraintError reports a DNS name that failed a name-constraint
// check. Excluded is true when the name matched an excluded constraint
// rather than failing to match any permitted constraint.
type DNSNameConstraintError struct {
	Name       string
	Constraint string
	Excluded   bool
}

func (e *DNSNameConstraintError) Error() string {
	if e.Excluded {
		return fmt.Sprintf("netsec: DNS name %q is excluded by constraint %q", e.Name, e.Constraint)
	}
	return fmt.Sprintf("netsec: DNS name %q is not permitted by any constraint", e.Name)
}

func (e *DNSNameConstraintError) Unwrap() error {
	if e.Excluded {
		return ErrDNSNameExcluded
	}
	return ErrDNSNameNotPermitted
}

// MatchDomainConstraint reports whether name satisfies constraint.
//
// It is an alias for MatchDNSNameConstraint, named after the equivalent
// check in the Go certificate verifier.
func MatchDomainConstraint(name, constraint string) (bool, error) {
	return MatchDNSNameConstraint(name, constraint)
}

// matchDomainConstraint mirrors the unexported helper used by the Go
// certificate verifier so package-local tests and integrations can reuse the
// same call shape.
func matchDomainConstraint(name, constraint string) (bool, error) {
	return MatchDNSNameConstraint(name, constraint)
}

// MatchDNSNameConstraint reports whether name satisfies the DNS name
// constraint.
//
// RFC 5280 expresses DNS constraints without a leading dot: the constraint
// "example.com" matches both "example.com" and every name below it, such as
// "a.example.com" or "a.b.example.com". Some callers still write
// ".example.com"; for DNS names the leading dot carries no additional
// meaning, so it is ignored. Consequently, ".example.com" matches
// "example.com" itself as well as its subdomains.
//
// Matching is case-insensitive and operates on complete labels. For example,
// "example.com" does not match "notexample.com" or "example.com.evil".
//
// An empty constraint matches every valid name: certificate implementations
// commonly use it to represent an unconstrained namespace. A constraint
// consisting only of a dot is invalid.
//
// A non-nil error reports an invalid DNS name or constraint.
func MatchDNSNameConstraint(name, constraint string) (bool, error) {
	domainLabels, err := reverseLabels(name)
	if err != nil {
		return false, fmt.Errorf("netsec: cannot parse DNS name %q: %w", name, err)
	}

	if constraint == "" {
		return true, nil
	}

	originalConstraint := constraint
	constraint = strings.TrimPrefix(constraint, ".")
	if constraint == "" {
		return false, fmt.Errorf("netsec: invalid DNS name constraint %q", originalConstraint)
	}

	constraintLabels, err := reverseLabels(constraint)
	if err != nil {
		return false, fmt.Errorf("netsec: cannot parse DNS name constraint %q: %w", originalConstraint, err)
	}

	if len(domainLabels) < len(constraintLabels) {
		return false, nil
	}

	for i, label := range constraintLabels {
		if !strings.EqualFold(label, domainLabels[i]) {
			return false, nil
		}
	}

	return true, nil
}

// DNSNameMatchesConstraint reports whether name satisfies constraint and
// treats invalid input as a non-match.
func DNSNameMatchesConstraint(name, constraint string) bool {
	matched, err := MatchDNSNameConstraint(name, constraint)
	return err == nil && matched
}

// CheckDNSNameConstraints checks name against permitted and excluded DNS
// constraint lists.
//
// Excluded constraints are checked first, matching certificate path
// validation. If permitted is empty, every name that is not excluded is
// permitted. Otherwise name must match at least one permitted constraint.
// A leading dot in any constraint is ignored, so ".example.com" and
// "example.com" permit the same namespace.
func CheckDNSNameConstraints(name string, permitted, excluded []string) error {
	if _, err := reverseLabels(name); err != nil {
		return fmt.Errorf("netsec: cannot parse DNS name %q: %w", name, err)
	}

	for _, constraint := range excluded {
		matched, err := MatchDNSNameConstraint(name, constraint)
		if err != nil {
			return fmt.Errorf("netsec: invalid excluded DNS name constraint %q: %w", constraint, err)
		}
		if matched {
			return &DNSNameConstraintError{Name: name, Constraint: constraint, Excluded: true}
		}
	}

	if len(permitted) == 0 {
		return nil
	}

	for _, constraint := range permitted {
		matched, err := MatchDNSNameConstraint(name, constraint)
		if err != nil {
			return fmt.Errorf("netsec: invalid permitted DNS name constraint %q: %w", constraint, err)
		}
		if matched {
			return nil
		}
	}

	return &DNSNameConstraintError{Name: name}
}

// CheckCertificateNameConstraints checks name against the DNS name
// constraints attached to cert. It is suitable for use from a certificate
// path builder or verifier.
func CheckCertificateNameConstraints(cert *x509.Certificate, name string) error {
	if cert == nil {
		return errors.New("netsec: nil certificate")
	}
	return CheckDNSNameConstraints(name, cert.PermittedDNSDomains, cert.ExcludedDNSDomains)
}

// reverseLabels converts a domain name into its labels in reverse order,
// e.g. "foo.example.com" becomes ["com", "example", "foo"].
func reverseLabels(domain string) ([]string, error) {
	var labels []string
	for len(domain) > 0 {
		if i := strings.LastIndexByte(domain, '.'); i == -1 {
			labels = append(labels, domain)
			domain = ""
		} else {
			labels = append(labels, domain[i+1:])
			domain = domain[:i]
			if i == 0 {
				labels = append(labels, "")
			}
		}
	}

	if len(labels) > 0 && len(labels[0]) == 0 {
		return nil, errors.New("absolute domain names are not supported")
	}
	for _, label := range labels {
		if len(label) == 0 {
			return nil, errors.New("empty label")
		}
		for _, c := range label {
			if c < 33 || c > 126 {
				return nil, errors.New("invalid character")
			}
		}
	}

	return labels, nil
}

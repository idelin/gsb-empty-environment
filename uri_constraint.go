package netsec

import (
	"crypto/x509"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strings"
)

var (
	// ErrNilURI is returned when a nil URI is passed to a matcher.
	ErrNilURI = errors.New("netsec: nil URI")
	// ErrEmptyURIHost is returned when a URI has no authority host.
	ErrEmptyURIHost = errors.New("netsec: URI has an empty host")
	// ErrInvalidURIHost is returned when a URI host is not a DNS name.
	ErrInvalidURIHost = errors.New("netsec: URI host is not a valid DNS name")
	// ErrIPURIHost is returned when a URI host is an IP address. URI name
	// constraints only apply to DNS names.
	ErrIPURIHost = errors.New("netsec: URI host is an IP address")
	// ErrEmptyURIConstraint is returned for an empty constraint.
	ErrEmptyURIConstraint = errors.New("netsec: empty URI constraint")
	// ErrInvalidURIConstraint is returned when a constraint host is malformed.
	ErrInvalidURIConstraint = errors.New("netsec: invalid URI constraint")
	// ErrExcludedURI is returned when a URI matches an excluded constraint.
	ErrExcludedURI = errors.New("netsec: URI matches an excluded constraint")
	// ErrURINotPermitted is returned when permitted constraints exist and
	// none of them matches the URI.
	ErrURINotPermitted = errors.New("netsec: URI does not match any permitted constraint")
)

// URIConstraint is a parsed host-only URI name constraint.
type URIConstraint struct {
	host          string
	subdomainOnly bool
}

// ParseURIConstraint parses a URI name constraint.
//
// A constraint may be a bare host (for example "example.com"), a bare host
// with a leading period (for example ".example.com"), or a complete URI
// (for example "https://example.com/ignored/path?q=ignored"). Only the host
// is retained. Path, query, fragment, user information, and port are
// discarded.
func ParseURIConstraint(raw string) (URIConstraint, error) {
	if raw == "" {
		return URIConstraint{}, ErrEmptyURIConstraint
	}

	host, err := constraintHost(raw)
	if err != nil {
		return URIConstraint{}, err
	}
	if host == "" {
		return URIConstraint{}, fmt.Errorf("%w: %q has no host", ErrInvalidURIConstraint, raw)
	}

	subdomainOnly := false
	if strings.HasPrefix(host, ".") {
		subdomainOnly = true
		host = host[1:]
	}

	if err := validateHost(host); err != nil {
		return URIConstraint{}, fmt.Errorf("%w: %q", ErrInvalidURIConstraint, raw)
	}

	return URIConstraint{host: host, subdomainOnly: subdomainOnly}, nil
}

// String returns the constraint in its canonical host form.
func (c URIConstraint) String() string {
	if c.subdomainOnly {
		return "." + c.host
	}
	return c.host
}

// Matches reports whether uri satisfies the constraint. Only the URI host is
// considered.
func (c URIConstraint) Matches(uri *url.URL) (bool, error) {
	host, err := URIHostname(uri)
	if err != nil {
		return false, err
	}
	return c.MatchesHost(host)
}

// Match is an alias for Matches.
func (c URIConstraint) Match(uri *url.URL) (bool, error) {
	return c.Matches(uri)
}

// MatchesHost reports whether host satisfies the constraint. host must be a
// DNS name, not an IP address.
func (c URIConstraint) MatchesHost(host string) (bool, error) {
	if err := validateHost(host); err != nil {
		return false, err
	}
	return matchHostLabels(host, c.host, c.subdomainOnly), nil
}

// MatchURIConstraint reports whether uri satisfies constraint.
//
// The URI is matched by host only. Changes to its path, query, fragment,
// user information, or port cannot change the result.
func MatchURIConstraint(uri *url.URL, constraint string) (bool, error) {
	c, err := ParseURIConstraint(constraint)
	if err != nil {
		return false, err
	}
	return c.Matches(uri)
}

// MatchURIConstraintString is the string form of MatchURIConstraint.
func MatchURIConstraintString(rawURI, constraint string) (bool, error) {
	uri, err := url.Parse(rawURI)
	if err != nil {
		return false, fmt.Errorf("netsec: parse URI %q: %w", rawURI, err)
	}
	return MatchURIConstraint(uri, constraint)
}

// MatchHostConstraint reports whether host satisfies constraint.
func MatchHostConstraint(host, constraint string) (bool, error) {
	c, err := ParseURIConstraint(constraint)
	if err != nil {
		return false, err
	}
	return c.MatchesHost(host)
}

// HostMatchesConstraint is the error-free form of MatchHostConstraint.
// Malformed hosts and constraints are reported as a non-match.
func HostMatchesConstraint(host, constraint string) bool {
	ok, err := MatchHostConstraint(host, constraint)
	return err == nil && ok
}

// URIHostname returns the DNS host of uri. It rejects URIs without a host and
// hosts that are IP addresses, because URI name constraints apply only to DNS
// names. Port, user information, path, query, and fragment are ignored.
func URIHostname(uri *url.URL) (string, error) {
	if uri == nil {
		return "", ErrNilURI
	}
	if uri.Host == "" {
		return "", fmt.Errorf("%w: %q", ErrEmptyURIHost, uri.String())
	}

	host := uri.Hostname()
	if host == "" {
		return "", fmt.Errorf("%w: %q", ErrEmptyURIHost, uri.String())
	}
	if err := validateHost(host); err != nil {
		return "", err
	}
	return host, nil
}

// URIConstraints is a parsed set of permitted and excluded URI constraints.
// An empty permitted set allows every host that is not excluded.
type URIConstraints struct {
	permitted []URIConstraint
	excluded  []URIConstraint
}

// NewURIConstraints parses permitted and excluded URI constraints.
func NewURIConstraints(permitted, excluded []string) (*URIConstraints, error) {
	c := &URIConstraints{
		permitted: make([]URIConstraint, 0, len(permitted)),
		excluded:  make([]URIConstraint, 0, len(excluded)),
	}

	for _, raw := range permitted {
		parsed, err := ParseURIConstraint(raw)
		if err != nil {
			return nil, fmt.Errorf("netsec: permitted URI constraint: %w", err)
		}
		c.permitted = append(c.permitted, parsed)
	}
	for _, raw := range excluded {
		parsed, err := ParseURIConstraint(raw)
		if err != nil {
			return nil, fmt.Errorf("netsec: excluded URI constraint: %w", err)
		}
		c.excluded = append(c.excluded, parsed)
	}
	return c, nil
}

// Permits reports whether uri is allowed by the constraint set.
func (c *URIConstraints) Permits(uri *url.URL) (bool, error) {
	if c == nil {
		return false, errors.New("netsec: nil URI constraints")
	}

	host, err := URIHostname(uri)
	if err != nil {
		return false, err
	}

	for _, constraint := range c.excluded {
		if matchHostLabels(host, constraint.host, constraint.subdomainOnly) {
			return false, nil
		}
	}
	if len(c.permitted) == 0 {
		return true, nil
	}
	for _, constraint := range c.permitted {
		if matchHostLabels(host, constraint.host, constraint.subdomainOnly) {
			return true, nil
		}
	}
	return false, nil
}

// Check returns nil when uri satisfies the constraint set, ErrExcludedURI when
// it matches an excluded constraint, and ErrURINotPermitted when permitted
// constraints exist but none matches.
func (c *URIConstraints) Check(uri *url.URL) error {
	if c == nil {
		return errors.New("netsec: nil URI constraints")
	}

	ok, err := c.Permits(uri)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}

	host, _ := URIHostname(uri)
	for _, constraint := range c.excluded {
		if matchHostLabels(host, constraint.host, constraint.subdomainOnly) {
			return fmt.Errorf("%w: host %q", ErrExcludedURI, host)
		}
	}
	return fmt.Errorf("%w: host %q", ErrURINotPermitted, host)
}

// CheckURIConstraints is a convenience wrapper that parses the constraint sets
// and checks uri in one call.
func CheckURIConstraints(uri *url.URL, permitted, excluded []string) error {
	c, err := NewURIConstraints(permitted, excluded)
	if err != nil {
		return err
	}
	return c.Check(uri)
}

// CheckCertificateURIConstraints checks every URI SAN in cert against the
// supplied constraint sets. This is suitable for certificate path validation
// code that has already collected the applicable URI name constraints.
func CheckCertificateURIConstraints(cert *x509.Certificate, permitted, excluded []string) error {
	if cert == nil {
		return errors.New("netsec: nil certificate")
	}
	c, err := NewURIConstraints(permitted, excluded)
	if err != nil {
		return err
	}
	for _, uri := range cert.URIs {
		if err := c.Check(uri); err != nil {
			return err
		}
	}
	return nil
}

func constraintHost(raw string) (string, error) {
	// First try an absolute or network-path URI. This preserves user
	// information long enough for net/url to remove it from the host.
	if uri, err := url.Parse(raw); err == nil && uri.Host != "" {
		return uri.Hostname(), nil
	}

	// A bare constraint such as "example.com", "example.com:443",
	// "example.com/ignored", or "user@example.com/ignored" needs an
	// authority marker before net/url will expose the host.
	uri, err := url.Parse("//" + raw)
	if err != nil {
		return "", fmt.Errorf("netsec: parse URI constraint %q: %w", raw, err)
	}
	if uri.Host == "" {
		return "", nil
	}
	return uri.Hostname(), nil
}

func validateHost(host string) error {
	if host == "" {
		return ErrEmptyURIHost
	}

	// Permit one trailing root dot, as is common for absolute DNS names,
	// but not multiple dots or an empty label.
	if strings.HasSuffix(host, ".") {
		host = strings.TrimSuffix(host, ".")
	}
	if host == "" {
		return ErrInvalidURIHost
	}

	// An IPv4 or IPv6 literal is never a DNS name. net/url removes the
	// brackets around IPv6 literals in URL.Hostname.
	if _, err := netip.ParseAddr(host); err == nil {
		return ErrIPURIHost
	}
	if strings.ContainsAny(host, ":/\\@?#") {
		return ErrInvalidURIHost
	}

	for _, label := range strings.Split(host, ".") {
		if label == "" {
			return ErrInvalidURIHost
		}
		for _, r := range label {
			// DNS names in certificates are IA5 strings. Restrict
			// matching to printable ASCII so unexpected Unicode
			// lookalikes cannot be normalized into a match.
			if r < 33 || r > 126 {
				return ErrInvalidURIHost
			}
		}
	}
	return nil
}

func matchHostLabels(host, constraint string, subdomainOnly bool) bool {
	hostLabels := splitDomainLabels(host)
	constraintLabels := splitDomainLabels(constraint)

	if len(hostLabels) < len(constraintLabels) {
		return false
	}
	if subdomainOnly && len(hostLabels) == len(constraintLabels) {
		return false
	}

	offset := len(hostLabels) - len(constraintLabels)
	for i, constraintLabel := range constraintLabels {
		if !strings.EqualFold(hostLabels[offset+i], constraintLabel) {
			return false
		}
	}
	return true
}

func splitDomainLabels(domain string) []string {
	if strings.HasSuffix(domain, ".") {
		domain = strings.TrimSuffix(domain, ".")
	}
	return strings.Split(domain, ".")
}

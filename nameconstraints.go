package netsec

import (
	"bytes"
	"crypto/x509"
	"encoding/asn1"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
)

// oidExtensionNameConstraints is id-ce-nameConstraints (2.5.29.30).
var oidExtensionNameConstraints = asn1.ObjectIdentifier{2, 5, 29, 30}

const defaultMaxConstraintComparisons = 250000

type nameConstraints struct {
	permittedDNS   []string
	excludedDNS    []string
	permittedEmail []string
	excludedEmail  []string
	permittedIP    []*net.IPNet
	excludedIP     []*net.IPNet
	permittedURI   []string
	excludedURI    []string
}

func constraintsOf(cert *x509.Certificate) nameConstraints {
	return nameConstraints{
		permittedDNS:   cert.PermittedDNSDomains,
		excludedDNS:    cert.ExcludedDNSDomains,
		permittedEmail: cert.PermittedEmailAddresses,
		excludedEmail:  cert.ExcludedEmailAddresses,
		permittedIP:    cert.PermittedIPRanges,
		excludedIP:     cert.ExcludedIPRanges,
		permittedURI:   cert.PermittedURIDomains,
		excludedURI:    cert.ExcludedURIDomains,
	}
}

// HasNameConstraints reports whether cert carries a name constraints
// extension, even if the extension only contains name types this package does
// not evaluate.
func HasNameConstraints(cert *x509.Certificate) bool {
	if cert == nil {
		return false
	}
	for _, ext := range cert.Extensions {
		if ext.Id.Equal(oidExtensionNameConstraints) {
			return true
		}
	}
	return false
}

// IsCA reports whether cert carries valid basic constraints asserting the CA
// role.
func IsCA(cert *x509.Certificate) bool {
	return cert != nil && cert.BasicConstraintsValid && cert.IsCA
}

type constraintChecker struct {
	comparisons int
	max         int
}

func newConstraintChecker(max int) *constraintChecker {
	if max <= 0 {
		max = defaultMaxConstraintComparisons
	}
	return &constraintChecker{max: max}
}

func (c *constraintChecker) count(n int) error {
	c.comparisons += n
	if c.comparisons > c.max {
		return ErrTooManyConstraints
	}
	return nil
}

// checkNameConstraintsFor enforces every constraint in ca against the names
// in cert. ca must be a CA and cert must be subordinate to it. Each CA in a
// path is checked independently, so a lower CA's wider permitted subtrees
// cannot erase a restriction imposed higher in the path.
func (c *constraintChecker) checkNameConstraintsFor(ca *x509.Certificate, caIndex int, cert *x509.Certificate, certIndex int) error {
	if ca == nil || cert == nil || !HasNameConstraints(ca) {
		return nil
	}

	nc := constraintsOf(ca)

	if len(nc.permittedDNS) > 0 || len(nc.excludedDNS) > 0 {
		for _, name := range cert.DNSNames {
			if err := c.checkDNSName(ca, caIndex, cert, certIndex, name, nc); err != nil {
				return err
			}
		}
	}

	if len(nc.permittedEmail) > 0 || len(nc.excludedEmail) > 0 {
		for _, name := range cert.EmailAddresses {
			if err := c.checkEmailName(ca, caIndex, cert, certIndex, name, nc); err != nil {
				return err
			}
		}
	}

	if len(nc.permittedIP) > 0 || len(nc.excludedIP) > 0 {
		for _, ip := range cert.IPAddresses {
			if err := c.checkIPName(ca, caIndex, cert, certIndex, ip, nc); err != nil {
				return err
			}
		}
	}

	if len(nc.permittedURI) > 0 || len(nc.excludedURI) > 0 {
		for _, name := range cert.URIs {
			if err := c.checkURIName(ca, caIndex, cert, certIndex, name, nc); err != nil {
				return err
			}
		}
	}

	return nil
}

func (c *constraintChecker) checkDNSName(ca *x509.Certificate, caIndex int, cert *x509.Certificate, certIndex int, name string, nc nameConstraints) error {
	if err := c.count(len(nc.excludedDNS)); err != nil {
		return validationError(certIndex, cert, err, "DNS name "+name)
	}
	for _, constraint := range nc.excludedDNS {
		matched, err := matchDomainConstraint(name, constraint)
		if err != nil {
			return validationError(certIndex, cert, ErrUnmatchableName, fmt.Sprintf("DNS name %q: %v", name, err))
		}
		if matched {
			return validationError(certIndex, cert, ErrNameExcluded,
				fmt.Sprintf("DNS name %q is excluded by CA %d (constraint %q)", name, caIndex, constraint))
		}
	}

	if len(nc.permittedDNS) == 0 {
		return nil
	}

	if err := c.count(len(nc.permittedDNS)); err != nil {
		return validationError(certIndex, cert, err, "DNS name "+name)
	}
	for _, constraint := range nc.permittedDNS {
		matched, err := matchDomainConstraint(name, constraint)
		if err != nil {
			return validationError(certIndex, cert, ErrUnmatchableName, fmt.Sprintf("DNS name %q: %v", name, err))
		}
		if matched {
			return nil
		}
	}

	return validationError(certIndex, cert, ErrNameNotPermitted,
		fmt.Sprintf("DNS name %q is not permitted by CA %d", name, caIndex))
}

func (c *constraintChecker) checkEmailName(ca *x509.Certificate, caIndex int, cert *x509.Certificate, certIndex int, name string, nc nameConstraints) error {
	if err := c.count(len(nc.excludedEmail)); err != nil {
		return validationError(certIndex, cert, err, "email address "+name)
	}
	for _, constraint := range nc.excludedEmail {
		matched, err := matchEmailConstraint(name, constraint)
		if err != nil {
			return validationError(certIndex, cert, ErrUnmatchableName, fmt.Sprintf("email address %q: %v", name, err))
		}
		if matched {
			return validationError(certIndex, cert, ErrNameExcluded,
				fmt.Sprintf("email address %q is excluded by CA %d (constraint %q)", name, caIndex, constraint))
		}
	}

	if len(nc.permittedEmail) == 0 {
		return nil
	}

	if err := c.count(len(nc.permittedEmail)); err != nil {
		return validationError(certIndex, cert, err, "email address "+name)
	}
	for _, constraint := range nc.permittedEmail {
		matched, err := matchEmailConstraint(name, constraint)
		if err != nil {
			return validationError(certIndex, cert, ErrUnmatchableName, fmt.Sprintf("email address %q: %v", name, err))
		}
		if matched {
			return nil
		}
	}

	return validationError(certIndex, cert, ErrNameNotPermitted,
		fmt.Sprintf("email address %q is not permitted by CA %d", name, caIndex))
}

func (c *constraintChecker) checkIPName(ca *x509.Certificate, caIndex int, cert *x509.Certificate, certIndex int, ip net.IP, nc nameConstraints) error {
	if err := c.count(len(nc.excludedIP)); err != nil {
		return validationError(certIndex, cert, err, "IP address "+ip.String())
	}
	for _, constraint := range nc.excludedIP {
		if matchIPConstraint(ip, constraint) {
			return validationError(certIndex, cert, ErrNameExcluded,
				fmt.Sprintf("IP address %q is excluded by CA %d (constraint %q)", ip, caIndex, constraint))
		}
	}

	if len(nc.permittedIP) == 0 {
		return nil
	}

	if err := c.count(len(nc.permittedIP)); err != nil {
		return validationError(certIndex, cert, err, "IP address "+ip.String())
	}
	for _, constraint := range nc.permittedIP {
		if matchIPConstraint(ip, constraint) {
			return nil
		}
	}

	return validationError(certIndex, cert, ErrNameNotPermitted,
		fmt.Sprintf("IP address %q is not permitted by CA %d", ip, caIndex))
}

func (c *constraintChecker) checkURIName(ca *x509.Certificate, caIndex int, cert *x509.Certificate, certIndex int, uri *url.URL, nc nameConstraints) error {
	host, err := uriConstraintHost(uri)
	if err != nil {
		return validationError(certIndex, cert, ErrUnmatchableName, err.Error())
	}

	if err := c.count(len(nc.excludedURI)); err != nil {
		return validationError(certIndex, cert, err, "URI "+uri.String())
	}
	for _, constraint := range nc.excludedURI {
		matched, err := matchDomainConstraint(host, constraint)
		if err != nil {
			return validationError(certIndex, cert, ErrUnmatchableName, fmt.Sprintf("URI %q: %v", uri, err))
		}
		if matched {
			return validationError(certIndex, cert, ErrNameExcluded,
				fmt.Sprintf("URI %q is excluded by CA %d (constraint %q)", uri, caIndex, constraint))
		}
	}

	if len(nc.permittedURI) == 0 {
		return nil
	}

	if err := c.count(len(nc.permittedURI)); err != nil {
		return validationError(certIndex, cert, err, "URI "+uri.String())
	}
	for _, constraint := range nc.permittedURI {
		matched, err := matchDomainConstraint(host, constraint)
		if err != nil {
			return validationError(certIndex, cert, ErrUnmatchableName, fmt.Sprintf("URI %q: %v", uri, err))
		}
		if matched {
			return nil
		}
	}

	return validationError(certIndex, cert, ErrNameNotPermitted,
		fmt.Sprintf("URI %q is not permitted by CA %d", uri, caIndex))
}

func uriConstraintHost(uri *url.URL) (string, error) {
	if uri == nil || !uri.IsAbs() {
		return "", fmt.Errorf("URI is not absolute")
	}

	host := uri.Hostname()
	if host == "" {
		return "", fmt.Errorf("URI %q has no host", uri)
	}

	if _, err := netip.ParseAddr(host); err == nil {
		return "", fmt.Errorf("URI %q uses an IP address host", uri)
	}

	return host, nil
}

func matchDomainConstraint(domain, constraint string) (bool, error) {
	// An empty constraint has historically been treated as matching every
	// name (the behaviour of NSS and Go's crypto/x509).
	if constraint == "" {
		return true, nil
	}

	// RFC 5280 gives a leading period special meaning for email and URI
	// constraints: the name must be a strict subdomain. This package also
	// accepts that spelling for DNS constraints.
	mustHaveSubdomains := false
	if constraint[0] == '.' {
		mustHaveSubdomains = true
		constraint = constraint[1:]
	}

	domainLabels, ok := domainToReverseLabels(domain)
	if !ok {
		return false, fmt.Errorf("cannot parse domain %q", domain)
	}
	constraintLabels, ok := domainToReverseLabels(constraint)
	if !ok {
		return false, fmt.Errorf("cannot parse constraint %q", constraint)
	}

	if len(domainLabels) < len(constraintLabels) ||
		(mustHaveSubdomains && len(domainLabels) == len(constraintLabels)) {
		return false, nil
	}

	for i, label := range constraintLabels {
		if !strings.EqualFold(label, domainLabels[i]) {
			return false, nil
		}
	}

	return true, nil
}

func domainToReverseLabels(domain string) ([]string, bool) {
	domain = strings.TrimSuffix(domain, ".")
	if domain == "" {
		return nil, false
	}

	labels := strings.Split(domain, ".")
	reversed := make([]string, len(labels))
	for i, label := range labels {
		if label == "" {
			return nil, false
		}
		for _, r := range label {
			if r < 33 || r > 126 {
				return nil, false
			}
		}
		reversed[len(labels)-1-i] = label
	}
	return reversed, true
}

func matchEmailConstraint(mailbox, constraint string) (bool, error) {
	local, domain, ok := splitMailbox(mailbox)
	if !ok {
		return false, fmt.Errorf("cannot parse mailbox %q", mailbox)
	}

	if strings.Contains(constraint, "@") {
		constraintLocal, constraintDomain, ok := splitMailbox(constraint)
		if !ok {
			return false, fmt.Errorf("cannot parse mailbox constraint %q", constraint)
		}
		return local == constraintLocal && strings.EqualFold(domain, constraintDomain), nil
	}

	return matchDomainConstraint(domain, constraint)
}

func splitMailbox(mailbox string) (local, domain string, ok bool) {
	if strings.Count(mailbox, "@") != 1 {
		return "", "", false
	}
	local, domain, found := strings.Cut(mailbox, "@")
	if !found || local == "" || domain == "" {
		return "", "", false
	}
	if _, ok := domainToReverseLabels(domain); !ok {
		return "", "", false
	}
	return local, domain, true
}

func matchIPConstraint(ip net.IP, constraint *net.IPNet) bool {
	if constraint == nil {
		return false
	}
	ip = normalizeIP(ip)
	network := normalizeIP(constraint.IP)
	if ip == nil || network == nil || len(ip) != len(network) {
		return false
	}

	mask := constraint.Mask
	if len(mask) != len(ip) {
		if len(mask) == net.IPv6len && len(ip) == net.IPv4len {
			mask = mask[12:]
		} else {
			return false
		}
	}

	for i := range ip {
		if ip[i]&mask[i] != network[i]&mask[i] {
			return false
		}
	}
	return true
}

func normalizeIP(ip net.IP) net.IP {
	if v4 := ip.To4(); v4 != nil {
		return v4
	}
	if v16 := ip.To16(); v16 != nil {
		return v16
	}
	return nil
}

func isSelfIssued(cert *x509.Certificate) bool {
	return bytes.Equal(cert.RawSubject, cert.RawIssuer)
}

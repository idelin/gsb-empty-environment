package netsec

import (
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// OIDNameConstraints is the id-ce-nameConstraints (2.5.29.30) extension OID.
var OIDNameConstraints = asn1.ObjectIdentifier{2, 5, 29, 30}

var (
	// ErrNoPermittedName reports a name that fails to match any
	// permittedSubtrees entry. A permittedSubtrees field that is present
	// but empty permits no names at all and always yields this error.
	ErrNoPermittedName = errors.New("netsec: name not allowed by permittedSubtrees")

	// ErrExcludedName reports a name that matches an excludedSubtrees entry.
	ErrExcludedName = errors.New("netsec: name matches excludedSubtrees")
)

// Subtree is a single GeneralSubtree base from a name constraints
// extension. Exactly one field is non-zero.
type Subtree struct {
	DNS   string
	Email string
	URI   string
	IP    *net.IPNet
}

// NameConstraints models an X.509 name constraints extension
// (RFC 5280, section 4.2.1.10).
//
// PermittedPresent records whether permittedSubtrees was present in the
// extension. The distinction matters: an absent permittedSubtrees imposes
// no restriction, while a present-but-empty permittedSubtrees permits no
// names at all. When permittedSubtrees is present, a checked name is
// allowed only if it falls within one of the listed subtrees of the same
// name form (fail closed).
type NameConstraints struct {
	PermittedPresent bool
	Permitted        []Subtree
	Excluded         []Subtree
}

// generalSubtree mirrors the ASN.1 GeneralSubtree production. The minimum
// and maximum BaseDistance fields are parsed but not enforced, per the
// RFC 5280 profile.
type generalSubtree struct {
	Base    asn1.RawValue
	Minimum int `asn1:"optional,tag:0,default:0"`
	Maximum int `asn1:"optional,tag:1"`
}

// Parse decodes a DER-encoded NameConstraints extension value.
func Parse(der []byte) (*NameConstraints, error) {
	var outer asn1.RawValue
	rest, err := asn1.Unmarshal(der, &outer)
	if err != nil {
		return nil, fmt.Errorf("netsec: cannot parse name constraints: %w", err)
	}
	if len(rest) != 0 {
		return nil, errors.New("netsec: trailing data after name constraints")
	}
	if outer.Class != asn1.ClassUniversal || outer.Tag != asn1.TagSequence || !outer.IsCompound {
		return nil, errors.New("netsec: name constraints is not a SEQUENCE")
	}

	nc := &NameConstraints{}
	content := outer.Bytes
	for len(content) > 0 {
		var field asn1.RawValue
		content, err = asn1.Unmarshal(content, &field)
		if err != nil {
			return nil, fmt.Errorf("netsec: cannot parse name constraints: %w", err)
		}
		if field.Class != asn1.ClassContextSpecific {
			return nil, fmt.Errorf("netsec: unexpected field with class %d in name constraints", field.Class)
		}
		switch field.Tag {
		case 0:
			// permittedSubtrees is present, even when it encodes an
			// empty GeneralSubtrees sequence.
			nc.PermittedPresent = true
			nc.Permitted, err = parseSubtrees(field.Bytes)
			if err != nil {
				return nil, fmt.Errorf("netsec: cannot parse permittedSubtrees: %w", err)
			}
		case 1:
			nc.Excluded, err = parseSubtrees(field.Bytes)
			if err != nil {
				return nil, fmt.Errorf("netsec: cannot parse excludedSubtrees: %w", err)
			}
		default:
			return nil, fmt.Errorf("netsec: unknown name constraints field tag %d", field.Tag)
		}
	}
	return nc, nil
}

// FromCertificate extracts and parses the name constraints extension from
// cert. It returns (nil, nil) when the certificate carries no name
// constraints extension.
func FromCertificate(cert *x509.Certificate) (*NameConstraints, error) {
	for _, ext := range cert.Extensions {
		if ext.Id.Equal(OIDNameConstraints) {
			return Parse(ext.Value)
		}
	}
	return nil, nil
}

func parseSubtrees(der []byte) ([]Subtree, error) {
	var subtrees []Subtree
	for len(der) > 0 {
		var gs generalSubtree
		var err error
		der, err = asn1.Unmarshal(der, &gs)
		if err != nil {
			return nil, err
		}
		st, err := parseGeneralName(gs.Base)
		if err != nil {
			return nil, err
		}
		if st == nil {
			continue
		}
		subtrees = append(subtrees, *st)
	}
	return subtrees, nil
}

// parseGeneralName converts a GeneralName CHOICE into a Subtree. It
// returns (nil, nil) for name forms this package does not enforce, such
// as directoryName.
func parseGeneralName(raw asn1.RawValue) (*Subtree, error) {
	if raw.Class != asn1.ClassContextSpecific {
		return nil, errors.New("general name is not context-specific")
	}
	switch raw.Tag {
	case 1: // rfc822Name
		return &Subtree{Email: string(raw.Bytes)}, nil
	case 2: // dNSName
		return &Subtree{DNS: string(raw.Bytes)}, nil
	case 6: // uniformResourceIdentifier
		return &Subtree{URI: string(raw.Bytes)}, nil
	case 7: // iPAddress
		ipNet, err := parseIPConstraint(raw.Bytes)
		if err != nil {
			return nil, err
		}
		return &Subtree{IP: ipNet}, nil
	default:
		return nil, nil
	}
}

func parseIPConstraint(b []byte) (*net.IPNet, error) {
	var ipLen int
	switch len(b) {
	case 4, 8:
		ipLen = net.IPv4len
	case 16, 32:
		ipLen = net.IPv6len
	default:
		return nil, fmt.Errorf("invalid IP constraint length %d", len(b))
	}
	mask := net.CIDRMask(ipLen*8, ipLen*8)
	if len(b) == 2*ipLen {
		mask = net.IPMask(b[ipLen:])
	}
	return &net.IPNet{IP: net.IP(b[:ipLen]), Mask: mask}, nil
}

// check applies the exclusion and permission rules. matcher reports
// whether a subtree constrains the name being checked; subtrees of other
// name forms never match.
func (nc *NameConstraints) check(matcher func(Subtree) bool) error {
	if nc == nil {
		return nil
	}
	for _, st := range nc.Excluded {
		if matcher(st) {
			return ErrExcludedName
		}
	}
	if !nc.PermittedPresent {
		return nil
	}
	for _, st := range nc.Permitted {
		if matcher(st) {
			return nil
		}
	}
	return ErrNoPermittedName
}

// CheckDNSName reports whether a DNS name is allowed by the constraints.
func (nc *NameConstraints) CheckDNSName(name string) error {
	name = normalizeDomain(name)
	return nc.check(func(st Subtree) bool {
		return st.DNS != "" && matchDomainConstraint(normalizeDomain(st.DNS), name)
	})
}

// CheckEmail reports whether an email address (rfc822Name) is allowed by
// the constraints.
func (nc *NameConstraints) CheckEmail(addr string) error {
	addr = strings.ToLower(addr)
	return nc.check(func(st Subtree) bool {
		if st.Email == "" {
			return false
		}
		constraint := strings.ToLower(st.Email)
		if strings.Contains(constraint, "@") {
			return addr == constraint
		}
		host := addr
		if i := strings.LastIndex(addr, "@"); i >= 0 {
			host = addr[i+1:]
		}
		return matchDomainConstraint(constraint, host)
	})
}

// CheckURI reports whether a URI is allowed by the constraints. The
// constraint applies to the URI's host component.
func (nc *NameConstraints) CheckURI(uri string) error {
	u, err := url.Parse(uri)
	if err != nil {
		return fmt.Errorf("netsec: cannot parse URI %q: %w", uri, err)
	}
	host := normalizeDomain(u.Hostname())
	return nc.check(func(st Subtree) bool {
		return st.URI != "" && matchDomainConstraint(normalizeDomain(st.URI), host)
	})
}

// CheckIP reports whether an IP address is allowed by the constraints.
func (nc *NameConstraints) CheckIP(ip net.IP) error {
	return nc.check(func(st Subtree) bool {
		return st.IP != nil && st.IP.Contains(ip)
	})
}

// CheckCertificate verifies that every subject alternative name in cert
// (DNS names, email addresses, URIs, and IP addresses) is allowed by the
// constraints. It is intended to be called during certificate path
// validation for each CA certificate in the chain that carries a name
// constraints extension.
func (nc *NameConstraints) CheckCertificate(cert *x509.Certificate) error {
	for _, name := range cert.DNSNames {
		if err := nc.CheckDNSName(name); err != nil {
			return fmt.Errorf("netsec: DNS name %q: %w", name, err)
		}
	}
	for _, email := range cert.EmailAddresses {
		if err := nc.CheckEmail(email); err != nil {
			return fmt.Errorf("netsec: email %q: %w", email, err)
		}
	}
	for _, uri := range cert.URIs {
		if err := nc.CheckURI(uri.String()); err != nil {
			return fmt.Errorf("netsec: URI %q: %w", uri, err)
		}
	}
	for _, ip := range cert.IPAddresses {
		if err := nc.CheckIP(ip); err != nil {
			return fmt.Errorf("netsec: IP %s: %w", ip, err)
		}
	}
	return nil
}

func normalizeDomain(name string) string {
	return strings.ToLower(strings.TrimSuffix(name, "."))
}

// matchDomainConstraint reports whether name falls within the domain
// subtree constraint. A constraint of "example.com" matches "example.com"
// and any subdomain such as "www.example.com"; a leading dot
// (".example.com") matches subdomains only. Both inputs must already be
// normalized with normalizeDomain.
func matchDomainConstraint(constraint, name string) bool {
	if constraint == "" || name == "" {
		return false
	}
	if constraint[0] == '.' {
		return strings.HasSuffix(name, constraint)
	}
	return name == constraint || strings.HasSuffix(name, "."+constraint)
}

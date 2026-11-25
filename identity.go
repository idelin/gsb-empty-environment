// Package workloadid implements an importable workload identity verification
// component for a Go service-mesh control plane.
//
// The component authenticates a workload from the full evidence set attached to
// its mTLS certificate:
//
//   - trust domain (derived from a validated SPIFFE URI SAN, never from DNS),
//   - SPIFFE path,
//   - the complete SAN set (DNS / IP / URI), with strict type separation,
//   - the verified signature chain and its validity window,
//   - intermediate-CA name constraints (RFC 5280),
//   - node attestation,
//   - revocation state (OCSP/CRL with grace windows and clock-rollback defense).
//
// Wildcard matching is deliberately narrow: a wildcard is only honoured when it
// is the complete, explicitly declared left-most DNS label, may not be combined
// with IDN punycode, and never matches multiple labels. This closes the
// "*.prod grants identity to evil.prod" class of over-granting bugs because
// identity is anchored on the SPIFFE trust domain plus an explicit policy
// rather than on a loose DNS suffix.
package workloadid

import (
	"crypto/x509"
	"errors"
	"net"
	"net/url"
	"strings"
)

// SANType enumerates the mutually exclusive SAN name spaces. A SAN of one type
// can never satisfy a policy rule of another type: DNS, IP and URI SANs are not
// interchangeable.
type SANType int

const (
	SANDNS SANType = iota + 1
	SANIP
	SANURI
)

func (t SANType) String() string {
	switch t {
	case SANDNS:
		return "dns"
	case SANIP:
		return "ip"
	case SANURI:
		return "uri"
	default:
		return "unknown"
	}
}

// SAN is one subjectAltName entry carried by a leaf certificate.
type SAN struct {
	Type SANType
	// Value is the normalized value: lower-cased DNS name, canonical IP string
	// or fully normalized URI string.
	Value string
}

// Identity is the authenticated identity of a workload.
type Identity struct {
	// SPIFFEID is the full spiffe://trust-domain/path identifier.
	SPIFFEID string
	// TrustDomain is the host component of the SPIFFE ID, lower cased.
	TrustDomain string
	// PathSegments holds the non-empty path segments of the SPIFFE path.
	PathSegments []string
	SANs         []SAN
	// Leaf is the presented leaf certificate.
	Leaf *x509.Certificate
}

// spiffeScheme is the only accepted URI scheme for a workload identity.
const spiffeScheme = "spiffe"

// maxSPIFFEIDLength mirrors the SPIFFE specification ceiling.
const maxSPIFFEIDLength = 2048

var (
	ErrNoSPIFFEID       = errors.New("workloadid: leaf carries no spiffe URI SAN")
	ErrMultipleSPIFFEID = errors.New("workloadid: leaf carries multiple spiffe URI SANs")
	ErrInvalidSPIFFEID  = errors.New("workloadid: invalid spiffe ID")
)

// ParseSPIFFE validates and splits a SPIFFE ID into trust domain and path
// segments according to https://github.com/spiffe/spiffe/blob/main/standards/SPIFFE.md.
func ParseSPIFFE(raw string) (trustDomain string, segments []string, err error) {
	if len(raw) > maxSPIFFEIDLength {
		return "", nil, ErrInvalidSPIFFEID
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", nil, ErrInvalidSPIFFEID
	}
	if u.Scheme != spiffeScheme {
		return "", nil, ErrInvalidSPIFFEID
	}
	if u.Opaque != "" || u.Host == "" {
		return "", nil, ErrInvalidSPIFFEID
	}
	// User info, query and fragment are never permitted.
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", nil, ErrInvalidSPIFFEID
	}
	host := strings.ToLower(u.Host)
	// Ports are not part of a trust domain.
	if h, _, splitErr := net.SplitHostPort(host); splitErr == nil {
		host = h
	}
	if !validTrustDomain(host) {
		return "", nil, ErrInvalidSPIFFEID
	}
	segments, err = splitSPIFFEPath(u.EscapedPath())
	if err != nil {
		return "", nil, err
	}
	return host, segments, nil
}

func validTrustDomain(host string) bool {
	if host == "" || len(host) > 255 {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return false
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 {
			return false
		}
		if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, r := range label {
			switch {
			case r >= 'a' && r <= 'z':
			case r >= '0' && r <= '9':
			case r == '-':
			default:
				return false
			}
		}
	}
	return true
}

func splitSPIFFEPath(escapedPath string) ([]string, error) {
	if escapedPath == "" || escapedPath == "/" {
		return nil, nil
	}
	if !strings.HasPrefix(escapedPath, "/") {
		return nil, ErrInvalidSPIFFEID
	}
	parts := strings.Split(escapedPath, "/")
	segments := make([]string, 0, len(parts)-1)
	for _, part := range parts[1:] {
		if part == "" {
			// Trailing or doubled slashes are not legal.
			return nil, ErrInvalidSPIFFEID
		}
		decoded, err := url.PathUnescape(part)
		if err != nil || decoded == "" || decoded == "." || decoded == ".." {
			return nil, ErrInvalidSPIFFEID
		}
		segments = append(segments, decoded)
	}
	return segments, nil
}

// buildIdentity extracts the identity from an already chain-validated leaf
// certificate. DNS, IP and URI SANs are kept in separate name spaces and never
// collapsed into one another.
func buildIdentity(leaf *x509.Certificate) (*Identity, error) {
	id := &Identity{Leaf: leaf}

	var spiffeIDs []string
	seen := make(map[string]struct{})

	for _, dns := range leaf.DNSNames {
		value := strings.ToLower(strings.TrimSpace(dns))
		if value == "" {
			continue
		}
		if _, ok := seen[sanKey(SANDNS, value)]; ok {
			continue
		}
		seen[sanKey(SANDNS, value)] = struct{}{}
		id.SANs = append(id.SANs, SAN{Type: SANDNS, Value: value})
	}
	for _, ip := range leaf.IPAddresses {
		value := ip.String()
		if _, ok := seen[sanKey(SANIP, value)]; ok {
			continue
		}
		seen[sanKey(SANIP, value)] = struct{}{}
		id.SANs = append(id.SANs, SAN{Type: SANIP, Value: value})
	}
	for _, rawURI := range leaf.URIs {
		if rawURI == nil {
			continue
		}
		value := strings.ToLower(strings.TrimSpace(rawURI.String()))
		if value == "" {
			continue
		}
		if rawURI.Scheme == spiffeScheme {
			spiffeIDs = append(spiffeIDs, value)
		}
		if _, ok := seen[sanKey(SANURI, value)]; ok {
			continue
		}
		seen[sanKey(SANURI, value)] = struct{}{}
		id.SANs = append(id.SANs, SAN{Type: SANURI, Value: value})
	}

	if len(spiffeIDs) == 0 {
		return nil, ErrNoSPIFFEID
	}
	if len(spiffeIDs) > 1 {
		return nil, ErrMultipleSPIFFEID
	}
	id.SPIFFEID = spiffeIDs[0]
	trustDomain, segments, err := ParseSPIFFE(id.SPIFFEID)
	if err != nil {
		return nil, err
	}
	id.TrustDomain = trustDomain
	id.PathSegments = segments
	return id, nil
}

func sanKey(t SANType, value string) string {
	return t.String() + "\x00" + value
}

// HasSAN reports whether the identity carries a SAN of the exact type and
// normalized value. Lookups are type scoped so a DNS rule can never be satisfied
// by a URI or IP SAN and vice versa.
func (id *Identity) HasSAN(t SANType, value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	for _, san := range id.SANs {
		if san.Type == t && san.Value == normalized {
			return true
		}
	}
	return false
}

// DNSNames returns only the DNS SAN values.
func (id *Identity) DNSNames() []string {
	var out []string
	for _, san := range id.SANs {
		if san.Type == SANDNS {
			out = append(out, san.Value)
		}
	}
	return out
}

// IPSANs returns only the IP SAN values.
func (id *Identity) IPSANs() []string {
	var out []string
	for _, san := range id.SANs {
		if san.Type == SANIP {
			out = append(out, san.Value)
		}
	}
	return out
}

// URISANs returns only the URI SAN values.
func (id *Identity) URISANs() []string {
	var out []string
	for _, san := range id.SANs {
		if san.Type == SANURI {
			out = append(out, san.Value)
		}
	}
	return out
}

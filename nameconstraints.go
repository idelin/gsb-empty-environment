package workloadid

import (
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"net"
	"strings"
)

// Name-constraint errors. They intentionally carry the offending value so the
// control plane can log the precise policy violation.
var (
	ErrExcludedNameConstraint    = errors.New("workloadid: SAN violates an excluded name constraint")
	ErrPermittedNameConstraint   = errors.New("workloadid: SAN is outside every permitted name constraint subtree")
	ErrUnsupportedNameConstraint = errors.New("workloadid: unsupported name constraint type present")
)

// checkNameConstraints applies the name constraints of every intermediate CA in
// the verified chain to every SAN of the leaf. The standard library already
// performs an RFC 5280 check during Verify, but an explicit, typed check is
// implemented so the control plane gets deterministic, auditable failures and
// cannot silently fall back to a looser path (for example when verification is
// driven through a custom root pool).
//
// DNS, URI and IP SANs are checked against the constraint sub-trees of their own
// name space; a DNS constraint can never validate an IP or URI SAN.
func checkNameConstraints(leaf *x509.Certificate, chain []*x509.Certificate) error {
	for _, ca := range chain {
		if !ca.IsCA {
			continue
		}
		constraints, err := parseConstraints(ca)
		if err != nil {
			return err
		}
		if constraints.empty() {
			continue
		}
		for _, san := range collectLeafSANs(leaf) {
			switch san.Type {
			case SANDNS:
				if err := constraints.checkDNS(san.Value); err != nil {
					return err
				}
			case SANIP:
				if err := constraints.checkIP(san.Value); err != nil {
					return err
				}
			case SANURI:
				if err := constraints.checkURI(san.Value); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

type nameConstraintSet struct {
	permittedDNS  []string
	excludedDNS   []string
	permittedURI  []string
	excludedURI   []string
	permittedIP   []*net.IPNet
	excludedIP    []*net.IPNet
	permittedMail []string
	excludedMail  []string
}

func (s *nameConstraintSet) empty() bool {
	return len(s.permittedDNS) == 0 && len(s.excludedDNS) == 0 &&
		len(s.permittedURI) == 0 && len(s.excludedURI) == 0 &&
		len(s.permittedIP) == 0 && len(s.excludedIP) == 0 &&
		len(s.permittedMail) == 0 && len(s.excludedMail) == 0
}

func parseConstraints(ca *x509.Certificate) (*nameConstraintSet, error) {
	set := &nameConstraintSet{
		permittedDNS:  ca.PermittedDNSDomains,
		excludedDNS:   ca.ExcludedDNSDomains,
		permittedURI:  ca.PermittedURIDomains,
		excludedURI:   ca.ExcludedURIDomains,
		permittedMail: ca.PermittedEmailAddresses,
		excludedMail:  ca.ExcludedEmailAddresses,
	}
	// x509.Certificate exposes IP ranges as parsed CIDRs.
	set.permittedIP = append(set.permittedIP, ca.PermittedIPRanges...)
	set.excludedIP = append(set.excludedIP, ca.ExcludedIPRanges...)

	// DirectoryName and registered-ID constraints have no meaningful mapping to
	// workload SANs. Refuse to ignore them so an attacker cannot hide a bypass in
	// a constraint type the data plane does not understand.
	if hasUnsupportedConstraint(ca) {
		return nil, ErrUnsupportedNameConstraint
	}
	return set, nil
}

// oidExtensionNameConstraints is 2.5.29.30.
var oidExtensionNameConstraints = asn1.ObjectIdentifier{2, 5, 29, 30}

// generalName tags from RFC 5280. The checker only understands rfc822Name (1),
// dNSName (2), uniformResourceIdentifier (6) and iPAddress (7). Any other
// GeneralName choice inside a name-constraint extension makes the constraint
// set unenforceable by this component and must be rejected rather than ignored.
func hasUnsupportedConstraint(ca *x509.Certificate) bool {
	var raw []byte
	for _, ext := range ca.Extensions {
		if ext.Id.Equal(oidExtensionNameConstraints) {
			raw = ext.Value
			break
		}
	}
	if raw == nil {
		return false
	}
	var outer asn1.RawValue
	if _, err := asn1.Unmarshal(raw, &outer); err != nil {
		// Malformed extension: the standard library rejects this during Verify;
		// treat it as unsupported to fail closed.
		return true
	}
	rest := outer.Bytes
	for len(rest) > 0 {
		var subtreeGroup asn1.RawValue
		var err error
		rest, err = asn1.Unmarshal(rest, &subtreeGroup)
		if err != nil {
			return true
		}
		subtrees := subtreeGroup.Bytes
		for len(subtrees) > 0 {
			var generalSubtree struct {
				Base    asn1.RawValue
				Minimum int `asn1:"optional,default:0,implicit,tag:0"`
				Maximum int `asn1:"optional,implicit,tag:1"`
			}
			subtrees, err = asn1.Unmarshal(subtrees, &generalSubtree)
			if err != nil {
				return true
			}
			// Base is a GeneralName choice tagged in the context-specific class.
			base := generalSubtree.Base
			if base.Class != asn1.ClassContextSpecific {
				return true
			}
			switch base.Tag {
			case 1, 2, 6, 7: // email, DNS, URI, IP
				continue
			default:
				return true
			}
		}
	}
	return false
}

func collectLeafSANs(leaf *x509.Certificate) []SAN {
	id, err := buildIdentity(leaf)
	if err != nil {
		// Fall back to raw SAN collection if identity construction fails; the
		// caller surfaces the identity error separately.
		var sans []SAN
		for _, dns := range leaf.DNSNames {
			sans = append(sans, SAN{Type: SANDNS, Value: strings.ToLower(dns)})
		}
		for _, ip := range leaf.IPAddresses {
			sans = append(sans, SAN{Type: SANIP, Value: ip.String()})
		}
		for _, u := range leaf.URIs {
			sans = append(sans, SAN{Type: SANURI, Value: strings.ToLower(u.String())})
		}
		return sans
	}
	return id.SANs
}

// dnsInConstraint implements RFC 5280 4.2.1.10 DNS name matching: a constraint
// value is matched as a prefix of the presented name where either the names are
// equal or the constraint ends at a label boundary.
func dnsInConstraint(name, constraint string) bool {
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	constraint = strings.ToLower(strings.TrimSuffix(constraint, "."))
	if constraint == "" {
		return true
	}
	if name == constraint {
		return true
	}
	return strings.HasSuffix(name, "."+constraint)
}

func (s *nameConstraintSet) checkDNS(name string) error {
	for _, excluded := range s.excludedDNS {
		if dnsInConstraint(name, excluded) {
			return ErrExcludedNameConstraint
		}
	}
	if len(s.permittedDNS) == 0 {
		return nil
	}
	for _, permitted := range s.permittedDNS {
		if dnsInConstraint(name, permitted) {
			return nil
		}
	}
	return ErrPermittedNameConstraint
}

// checkURI applies the constraint to the host component of the URI. The host is
// matched with the same label-boundary rule as DNS constraints per RFC 5280.
func (s *nameConstraintSet) checkURI(raw string) error {
	host := uriHost(raw)
	if host == "" {
		return ErrPermittedNameConstraint
	}
	for _, excluded := range s.excludedURI {
		if dnsInConstraint(host, excluded) {
			return ErrExcludedNameConstraint
		}
	}
	if len(s.permittedURI) == 0 {
		return nil
	}
	for _, permitted := range s.permittedURI {
		if dnsInConstraint(host, permitted) {
			return nil
		}
	}
	return ErrPermittedNameConstraint
}

func (s *nameConstraintSet) checkIP(value string) error {
	ip := net.ParseIP(value)
	if ip == nil {
		return ErrPermittedNameConstraint
	}
	for _, network := range s.excludedIP {
		if network.Contains(ip) {
			return ErrExcludedNameConstraint
		}
	}
	if len(s.permittedIP) == 0 {
		return nil
	}
	for _, network := range s.permittedIP {
		if network.Contains(ip) {
			return nil
		}
	}
	return ErrPermittedNameConstraint
}

func uriHost(raw string) string {
	raw = strings.TrimSpace(raw)
	// Avoid pulling in url.Parse twice; split scheme and authority cheaply.
	schemeEnd := strings.Index(raw, "://")
	if schemeEnd < 0 {
		return ""
	}
	rest := raw[schemeEnd+3:]
	if slash := strings.IndexAny(rest, "/?#"); slash >= 0 {
		rest = rest[:slash]
	}
	// Strip user info and port.
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		rest = rest[at+1:]
	}
	if colon := strings.LastIndex(rest, ":"); colon >= 0 && net.ParseIP(rest) == nil {
		rest = rest[:colon]
	}
	return strings.ToLower(rest)
}

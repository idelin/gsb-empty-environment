package netsec

import (
	"bytes"
	"crypto/subtle"
	"crypto/x509"
	"fmt"
	"net"
	"strings"
)

// IKEv2 identification payload types (RFC 7296, section 3.5).
const (
	IDIPv4Addr   byte = 1
	IDFQDN       byte = 2
	IDRFC822Addr byte = 3
	IDIPv6Addr   byte = 5
	IDDERASN1DN  byte = 9
	IDDERASN1GN  byte = 10
	IDKeyID      byte = 11
)

// Identity is a decoded IKEv2 identification payload.
type Identity struct {
	Type  byte
	Value []byte
}

// NewIdentity creates an identity from a type and value.
func NewIdentity(idType byte, value []byte) Identity {
	return Identity{Type: idType, Value: append([]byte(nil), value...)}
}

// FQDNIdentity creates an ID_FQDN identity.
func FQDNIdentity(name string) Identity {
	return Identity{Type: IDFQDN, Value: []byte(name)}
}

// IPv4Identity creates an ID_IPV4_ADDR identity.
func IPv4Identity(ip net.IP) (Identity, error) {
	ip4 := ip.To4()
	if ip4 == nil {
		return Identity{}, fmt.Errorf("%w: not an IPv4 address", ErrMalformedIdentity)
	}
	return Identity{Type: IDIPv4Addr, Value: append([]byte(nil), ip4...)}, nil
}

// IPv6Identity creates an ID_IPV6_ADDR identity.
func IPv6Identity(ip net.IP) (Identity, error) {
	ip16 := ip.To16()
	if ip16 == nil || ip.To4() != nil {
		return Identity{}, fmt.Errorf("%w: not an IPv6 address", ErrMalformedIdentity)
	}
	return Identity{Type: IDIPv6Addr, Value: append([]byte(nil), ip16...)}, nil
}

// PayloadBody encodes the identity as an IKEv2 ID payload body:
// IDType || 0 || 0 || 0 || IdentificationData.
func (i Identity) PayloadBody() []byte {
	out := make([]byte, 4+len(i.Value))
	out[0] = i.Type
	copy(out[4:], i.Value)
	return out
}

// ParseIdentityPayloadBody decodes a complete IKEv2 ID payload body.
func ParseIdentityPayloadBody(body []byte) (Identity, error) {
	if len(body) < 4 {
		return Identity{}, fmt.Errorf("%w: body shorter than 4 octets", ErrMalformedIdentity)
	}
	idType := body[0]
	if !validIDType(idType) {
		return Identity{}, fmt.Errorf("%w: unknown ID type %d", ErrMalformedIdentity, idType)
	}
	if len(body) == 4 {
		return Identity{}, fmt.Errorf("%w: empty identification data", ErrMalformedIdentity)
	}
	return Identity{
		Type:  idType,
		Value: append([]byte(nil), body[4:]...),
	}, nil
}

// IdentityFromPayloadBody is an alias for ParseIdentityPayloadBody.
func IdentityFromPayloadBody(body []byte) (Identity, error) {
	return ParseIdentityPayloadBody(body)
}

func validIDType(idType byte) bool {
	switch idType {
	case IDIPv4Addr, IDFQDN, IDRFC822Addr, IDIPv6Addr,
		IDDERASN1DN, IDDERASN1GN, IDKeyID:
		return true
	default:
		return false
	}
}

// MatchIdentityToCertificate checks a complete ID payload body against the
// subjectAltName extension of cert. The certificate's subject DN is not used.
//
// ID_FQDN is matched against dNSName SANs (including valid wildcard SANs),
// ID_IPV4_ADDR and ID_IPV6_ADDR against iPAddress SANs, and ID_RFC822_ADDR
// against rfc822Name SANs. Identity types that are not represented by an X.509
// SAN are rejected.
func MatchIdentityToCertificate(body []byte, cert *x509.Certificate) error {
	if cert == nil {
		return ErrCertificateRequired
	}
	id, err := ParseIdentityPayloadBody(body)
	if err != nil {
		return err
	}
	return MatchIdentityToCertificateID(id, cert)
}

// MatchIdentityToCertificateID is the decoded-identity form of
// MatchIdentityToCertificate.
func MatchIdentityToCertificateID(id Identity, cert *x509.Certificate) error {
	if cert == nil {
		return ErrCertificateRequired
	}
	if len(id.Value) == 0 {
		return fmt.Errorf("%w: empty identification data", ErrMalformedIdentity)
	}

	switch id.Type {
	case IDFQDN:
		name := string(id.Value)
		if !validDNSName(name) {
			return fmt.Errorf("%w: invalid FQDN", ErrIdentityMismatch)
		}
		for _, san := range cert.DNSNames {
			if dnsSANMatches(san, name) {
				return nil
			}
		}
		return fmt.Errorf("%w: ID_FQDN %q not present in certificate SANs", ErrIdentityMismatch, name)

	case IDIPv4Addr:
		if len(id.Value) != net.IPv4len {
			return fmt.Errorf("%w: IPv4 identity has length %d", ErrMalformedIdentity, len(id.Value))
		}
		ip := net.IP(id.Value)
		for _, san := range cert.IPAddresses {
			if san.Equal(ip) {
				return nil
			}
		}
		return fmt.Errorf("%w: IPv4 identity %s not present in certificate SANs", ErrIdentityMismatch, ip)

	case IDIPv6Addr:
		if len(id.Value) != net.IPv6len {
			return fmt.Errorf("%w: IPv6 identity has length %d", ErrMalformedIdentity, len(id.Value))
		}
		ip := net.IP(id.Value)
		for _, san := range cert.IPAddresses {
			if san.Equal(ip) {
				return nil
			}
		}
		return fmt.Errorf("%w: IPv6 identity %s not present in certificate SANs", ErrIdentityMismatch, ip)

	case IDRFC822Addr:
		email := string(id.Value)
		for _, san := range cert.EmailAddresses {
			if emailSANMatches(san, email) {
				return nil
			}
		}
		return fmt.Errorf("%w: ID_RFC822_ADDR %q not present in certificate SANs", ErrIdentityMismatch, email)

	case IDDERASN1DN, IDDERASN1GN, IDKeyID:
		return fmt.Errorf("%w: ID type %d has no X.509 SAN representation", ErrUnsupportedIdentity, id.Type)
	default:
		return fmt.Errorf("%w: ID type %d", ErrUnsupportedIdentity, id.Type)
	}
}

func validDNSName(name string) bool {
	if name == "" || strings.ContainsAny(name, "\x00\r\n\t ") {
		return false
	}
	if strings.Contains(name, "*") {
		return false
	}
	return true
}

func dnsSANMatches(san, identity string) bool {
	san = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(san)), ".")
	identity = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(identity)), ".")
	if san == "" || identity == "" {
		return false
	}
	if san == identity {
		return true
	}

	if !strings.HasPrefix(san, "*.") {
		return false
	}
	suffix := san[2:]
	if suffix == "" || strings.Contains(suffix, "*") {
		return false
	}
	if !strings.HasSuffix(identity, "."+suffix) {
		return false
	}
	label := strings.TrimSuffix(identity, "."+suffix)
	return label != "" && !strings.Contains(label, ".") && !strings.Contains(label, "*")
}

func emailSANMatches(san, identity string) bool {
	san = strings.TrimSpace(san)
	identity = strings.TrimSpace(identity)
	if san == "" || identity == "" {
		return false
	}
	atSAN := strings.LastIndexByte(san, '@')
	atID := strings.LastIndexByte(identity, '@')
	if atSAN < 0 || atID < 0 {
		return strings.EqualFold(san, identity)
	}
	if san[:atSAN] != identity[:atID] {
		return false
	}
	return strings.EqualFold(san[atSAN+1:], identity[atID+1:])
}

// identitiesEqual compares two ID payload bodies without leaking timing
// information. FQDNs, e-mail domains, and IP addresses are compared
// semantically so that equivalent encodings cannot bypass reflection checks.
func identitiesEqual(a, b []byte) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	if subtle.ConstantTimeCompare(a, b) == 1 {
		return true
	}

	aID, aErr := ParseIdentityPayloadBody(a)
	bID, bErr := ParseIdentityPayloadBody(b)
	if aErr != nil || bErr != nil || aID.Type != bID.Type {
		return false
	}
	switch aID.Type {
	case IDFQDN:
		return strings.EqualFold(
			strings.TrimSuffix(string(aID.Value), "."),
			strings.TrimSuffix(string(bID.Value), "."),
		)
	case IDRFC822Addr:
		return emailSANMatches(string(aID.Value), string(bID.Value))
	case IDIPv4Addr, IDIPv6Addr:
		if len(aID.Value) != len(bID.Value) {
			return false
		}
		return bytes.Equal(aID.Value, bID.Value)
	default:
		return subtle.ConstantTimeCompare(aID.Value, bID.Value) == 1
	}
}

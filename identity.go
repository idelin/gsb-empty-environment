package netsec

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/hex"
	"fmt"
	"net"
	"strings"
)

// Fingerprint is the SHA-256 digest of a certificate's DER encoding.
type Fingerprint [sha256.Size]byte

// FingerprintOf returns the SHA-256 fingerprint of cert's DER bytes.
func FingerprintOf(cert *x509.Certificate) Fingerprint {
	if cert == nil || len(cert.Raw) == 0 {
		return Fingerprint{}
	}
	return FingerprintOfDER(cert.Raw)
}

// FingerprintOfDER returns the SHA-256 fingerprint of a DER certificate.
func FingerprintOfDER(der []byte) Fingerprint {
	return sha256.Sum256(der)
}

// String returns the lower-case hexadecimal fingerprint.
func (f Fingerprint) String() string {
	return hex.EncodeToString(f[:])
}

// IsZero reports whether the fingerprint is the all-zero value.
func (f Fingerprint) IsZero() bool {
	return f == Fingerprint{}
}

// ParseFingerprint parses a SHA-256 fingerprint. It accepts upper- or
// lower-case hexadecimal, optional colon separators, and an optional
// "sha256:" prefix.
func ParseFingerprint(s string) (Fingerprint, error) {
	var fp Fingerprint
	s = strings.TrimSpace(s)
	if len(s) >= len("sha256:") && strings.EqualFold(s[:len("sha256:")], "sha256:") {
		s = s[len("sha256:"):]
	}
	s = strings.ReplaceAll(s, ":", "")
	raw, err := hex.DecodeString(s)
	if err != nil {
		return fp, fmt.Errorf("netsec: parse fingerprint: %w", err)
	}
	if len(raw) != len(fp) {
		return fp, fmt.Errorf("netsec: fingerprint must be %d bytes, got %d", len(fp), len(raw))
	}
	copy(fp[:], raw)
	return fp, nil
}

// IdentitySource records which class of subject identity produced a mapping.
type IdentitySource string

const (
	// IdentityByUniqueID means a unique subject attribute such as
	// subject.serialNumber, organizationIdentifier, or UID was used.
	IdentityByUniqueID IdentitySource = "unique-id"
	// IdentityBySAN means a DNS, URI, email, or IP subjectAltName was used.
	IdentityBySAN IdentitySource = "san"
	// IdentityByCN means the commonName fallback was used. It is only
	// available when the CN is registered to exactly one tenant and the
	// issuer has already passed trust validation.
	IdentityByCN IdentitySource = "cn"
)

const (
	SourceUniqueID = IdentityByUniqueID
	SourceSAN      = IdentityBySAN
	SourceCN       = IdentityByCN
)

var oidSubjectAltName = asn1.ObjectIdentifier{2, 5, 29, 17}

type identityClaim struct {
	key    string
	source IdentitySource
	kind   string
	value  string
}

func (c identityClaim) label() string {
	return c.kind + "=" + c.value
}

type certIdentity struct {
	uniqueIDs []identityClaim
	sans      []identityClaim
	cn        string
	hasSANExt bool
}

// defaultUniqueIDOIDs contains subject attributes that are intended to name a
// subject uniquely. CN (2.5.4.3) is deliberately absent.
var defaultUniqueIDOIDs = []asn1.ObjectIdentifier{
	{2, 5, 4, 5},                      // serialNumber
	{2, 5, 4, 45},                     // x500UniqueIdentifier
	{2, 5, 4, 97},                     // organizationIdentifier
	{0, 9, 2342, 19200300, 100, 1, 1}, // UID
}

func claimKey(source IdentitySource, kind, value string) string {
	return string(source) + "\x00" + kind + "\x00" + value
}

func normalizeCN(cn string) string {
	return strings.ToLower(strings.TrimSpace(cn))
}

func normalizeDNS(name string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func attributeString(value any) (string, bool) {
	switch value := value.(type) {
	case string:
		return value, true
	case []byte:
		return hex.EncodeToString(value), true
	case asn1.BitString:
		return hex.EncodeToString(value.Bytes), true
	default:
		return "", false
	}
}

func extractIdentity(cert *x509.Certificate, additionalUniqueOIDs []asn1.ObjectIdentifier) (certIdentity, error) {
	var id certIdentity
	if cert == nil {
		return id, ErrCertificateRequired
	}

	uniqueOIDs := make(map[string]struct{}, len(defaultUniqueIDOIDs)+len(additionalUniqueOIDs))
	for _, oid := range defaultUniqueIDOIDs {
		uniqueOIDs[oid.String()] = struct{}{}
	}
	for _, oid := range additionalUniqueOIDs {
		if oid != nil {
			uniqueOIDs[oid.String()] = struct{}{}
		}
	}

	seenUnique := make(map[string]struct{})
	addUnique := func(oid, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		key := claimKey(IdentityByUniqueID, oid, value)
		if _, ok := seenUnique[key]; ok {
			return
		}
		seenUnique[key] = struct{}{}
		id.uniqueIDs = append(id.uniqueIDs, identityClaim{
			key:    key,
			source: IdentityByUniqueID,
			kind:   oid,
			value:  value,
		})
	}

	if cert.Subject.SerialNumber != "" {
		addUnique("2.5.4.5", cert.Subject.SerialNumber)
	}
	for _, name := range cert.Subject.Names {
		if _, ok := uniqueOIDs[name.Type.String()]; !ok {
			continue
		}
		if value, ok := attributeString(name.Value); ok {
			addUnique(name.Type.String(), value)
		}
	}
	for _, name := range cert.Subject.ExtraNames {
		if _, ok := uniqueOIDs[name.Type.String()]; !ok {
			continue
		}
		if value, ok := attributeString(name.Value); ok {
			addUnique(name.Type.String(), value)
		}
	}

	seenSAN := make(map[string]struct{})
	addSAN := func(kind, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		key := claimKey(IdentityBySAN, kind, value)
		if _, ok := seenSAN[key]; ok {
			return
		}
		seenSAN[key] = struct{}{}
		id.sans = append(id.sans, identityClaim{
			key:    key,
			source: IdentityBySAN,
			kind:   kind,
			value:  value,
		})
	}

	for _, name := range cert.DNSNames {
		addSAN("dns", normalizeDNS(name))
	}
	for _, email := range cert.EmailAddresses {
		addSAN("email", normalizeEmail(email))
	}
	for _, ip := range cert.IPAddresses {
		if ip != nil {
			addSAN("ip", net.IP(ip).String())
		}
	}
	for _, uri := range cert.URIs {
		if uri != nil {
			addSAN("uri", uri.String())
		}
	}

	for _, ext := range cert.Extensions {
		if ext.Id.Equal(oidSubjectAltName) {
			id.hasSANExt = true
			break
		}
	}

	id.cn = normalizeCN(cert.Subject.CommonName)
	if len(id.uniqueIDs) == 0 && len(id.sans) == 0 && id.cn == "" {
		return certIdentity{}, ErrNoUsableIdentity
	}
	if len(id.uniqueIDs) == 0 && len(id.sans) == 0 && id.hasSANExt {
		return certIdentity{}, ErrUnsupportedSAN
	}
	return id, nil
}

func (id certIdentity) strongClaims() []identityClaim {
	claims := make([]identityClaim, 0, len(id.uniqueIDs)+len(id.sans))
	claims = append(claims, id.uniqueIDs...)
	claims = append(claims, id.sans...)
	return claims
}

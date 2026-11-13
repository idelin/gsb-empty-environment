package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// SignatureScheme is a TLS SignatureScheme value as defined by RFC 8446,
// Section 4.2.3. The values are also used in the signature_algorithms and
// signature_algorithms_cert extensions.
type SignatureScheme uint16

const (
	// RSASSA-PKCS1-v1_5 algorithms.
	PKCS1WithSHA1   SignatureScheme = 0x0201
	PKCS1WithSHA224 SignatureScheme = 0x0301
	PKCS1WithSHA256 SignatureScheme = 0x0401
	PKCS1WithSHA384 SignatureScheme = 0x0501
	PKCS1WithSHA512 SignatureScheme = 0x0601

	// DSA algorithms. These are legacy algorithms retained for certificate
	// signature compatibility.
	DSAWithSHA1   SignatureScheme = 0x0202
	DSAWithSHA224 SignatureScheme = 0x0302
	DSAWithSHA256 SignatureScheme = 0x0402

	// ECDSA algorithms.
	ECDSAWithSHA1          SignatureScheme = 0x0203
	ECDSAWithSHA224        SignatureScheme = 0x0303
	ECDSAWithP256AndSHA256 SignatureScheme = 0x0403
	ECDSAWithP384AndSHA384 SignatureScheme = 0x0503
	ECDSAWithP521AndSHA512 SignatureScheme = 0x0603

	// RSASSA-PSS algorithms.
	PSSWithSHA256 SignatureScheme = 0x0804
	PSSWithSHA384 SignatureScheme = 0x0805
	PSSWithSHA512 SignatureScheme = 0x0806

	// EdDSA algorithms.
	Ed25519 SignatureScheme = 0x0807
	Ed448   SignatureScheme = 0x0808
)

// Alternative spellings used by several TLS implementations. They are aliases
// of the canonical crypto/tls-style names above.
const (
	RSA_PKCS1_SHA1   = PKCS1WithSHA1
	RSA_PKCS1_SHA224 = PKCS1WithSHA224
	RSA_PKCS1_SHA256 = PKCS1WithSHA256
	RSA_PKCS1_SHA384 = PKCS1WithSHA384
	RSA_PKCS1_SHA512 = PKCS1WithSHA512

	RSA_PSS_SHA256 = PSSWithSHA256
	RSA_PSS_SHA384 = PSSWithSHA384
	RSA_PSS_SHA512 = PSSWithSHA512

	DSA_SHA1   = DSAWithSHA1
	DSA_SHA224 = DSAWithSHA224
	DSA_SHA256 = DSAWithSHA256

	ECDSA_SHA1        = ECDSAWithSHA1
	ECDSA_SHA224      = ECDSAWithSHA224
	ECDSA_P256_SHA256 = ECDSAWithP256AndSHA256
	ECDSA_P384_SHA384 = ECDSAWithP384AndSHA384
	ECDSA_P521_SHA512 = ECDSAWithP521AndSHA512

	ECDSA_SECP256R1_SHA256 = ECDSAWithP256AndSHA256
	ECDSA_SECP384R1_SHA384 = ECDSAWithP384AndSHA384
	ECDSA_SECP521R1_SHA512 = ECDSAWithP521AndSHA512
)

// SignatureScope describes where a SignatureScheme may be used. A certificate
// signature and a handshake signature are negotiated from different scopes.
type SignatureScope uint8

const (
	ScopeNone        SignatureScope = 0
	ScopeHandshake   SignatureScope = 1 << 0
	ScopeCertificate SignatureScope = 1 << 1
	ScopeBoth                       = ScopeHandshake | ScopeCertificate
)

// Has reports whether sc includes scope.
func (sc SignatureScope) Has(scope SignatureScope) bool {
	return sc&scope == scope
}

// String returns a human-readable name for the scheme.
func (s SignatureScheme) String() string {
	switch s {
	case PKCS1WithSHA1:
		return "rsa_pkcs1_sha1"
	case PKCS1WithSHA224:
		return "rsa_pkcs1_sha224"
	case PKCS1WithSHA256:
		return "rsa_pkcs1_sha256"
	case PKCS1WithSHA384:
		return "rsa_pkcs1_sha384"
	case PKCS1WithSHA512:
		return "rsa_pkcs1_sha512"
	case DSAWithSHA1:
		return "dsa_sha1"
	case DSAWithSHA224:
		return "dsa_sha224"
	case DSAWithSHA256:
		return "dsa_sha256"
	case ECDSAWithSHA1:
		return "ecdsa_sha1"
	case ECDSAWithSHA224:
		return "ecdsa_sha224"
	case ECDSAWithP256AndSHA256:
		return "ecdsa_secp256r1_sha256"
	case ECDSAWithP384AndSHA384:
		return "ecdsa_secp384r1_sha384"
	case ECDSAWithP521AndSHA512:
		return "ecdsa_secp521r1_sha512"
	case PSSWithSHA256:
		return "rsa_pss_rsae_sha256"
	case PSSWithSHA384:
		return "rsa_pss_rsae_sha384"
	case PSSWithSHA512:
		return "rsa_pss_rsae_sha512"
	case Ed25519:
		return "ed25519"
	case Ed448:
		return "ed448"
	default:
		return fmt.Sprintf("SignatureScheme(0x%04x)", uint16(s))
	}
}

// Scopes returns the protocol-independent scopes for known schemes. The result
// is intentionally permissive for unknown/private-use values: whether such a
// value is usable is decided by the two peers' advertised lists.
func (s SignatureScheme) Scopes() SignatureScope {
	if s == 0 {
		return ScopeNone
	}
	return ScopeBoth
}

// TLS13Scopes returns the scope of the scheme under TLS 1.3. TLS 1.3 only
// permits RSASSA-PSS, curve-bound ECDSA, and EdDSA for CertificateVerify.
// PKCS#1 v1.5 and SHA-1 based schemes remain usable for signatures that
// already exist in certificates, but not for handshake signatures.
func (s SignatureScheme) TLS13Scopes() SignatureScope {
	if s == 0 {
		return ScopeNone
	}

	switch s {
	case PSSWithSHA256, PSSWithSHA384, PSSWithSHA512,
		ECDSAWithP256AndSHA256, ECDSAWithP384AndSHA384, ECDSAWithP521AndSHA512,
		Ed25519, Ed448:
		return ScopeBoth

	case PKCS1WithSHA1, PKCS1WithSHA224, PKCS1WithSHA256, PKCS1WithSHA384, PKCS1WithSHA512,
		DSAWithSHA1, DSAWithSHA224, DSAWithSHA256,
		ECDSAWithSHA1, ECDSAWithSHA224:
		return ScopeCertificate

	default:
		// Unknown values are not classified locally. Keep them in the
		// certificate scope so forward-compatible schemes can still be
		// intersected with an explicit local support list.
		return ScopeBoth
	}
}

// IsCertificateSignatureScheme reports whether the scheme can be used for a
// signature in a certificate.
func (s SignatureScheme) IsCertificateSignatureScheme() bool {
	return s.TLS13Scopes().Has(ScopeCertificate)
}

// IsHandshakeSignatureScheme reports whether the scheme can be used for a
// CertificateVerify signature under TLS 1.3.
func (s SignatureScheme) IsHandshakeSignatureScheme() bool {
	return s.TLS13Scopes().Has(ScopeHandshake)
}

// IsTLS13CertificateScheme is an alias for IsCertificateSignatureScheme.
func (s SignatureScheme) IsTLS13CertificateScheme() bool {
	return s.IsCertificateSignatureScheme()
}

// IsTLS13HandshakeScheme is an alias for IsHandshakeSignatureScheme.
func (s SignatureScheme) IsTLS13HandshakeScheme() bool {
	return s.IsHandshakeSignatureScheme()
}

// CertificateScopeSchemes returns the entries of schemes that are permitted in
// certificate signatures under TLS 1.3, preserving their input order.
//
// This helper is useful for a stricter fallback policy. The negotiated
// signature_algorithms_cert behavior itself is implemented by
// ApplicableCertificateSchemes.
func CertificateScopeSchemes(schemes []SignatureScheme) []SignatureScheme {
	if len(schemes) == 0 {
		return nil
	}
	out := make([]SignatureScheme, 0, len(schemes))
	for _, scheme := range schemes {
		if scheme.IsCertificateSignatureScheme() {
			out = append(out, scheme)
		}
	}
	return out
}

// HandshakeScopeSchemes returns the entries of schemes that are permitted for
// TLS 1.3 CertificateVerify signatures, preserving their input order.
func HandshakeScopeSchemes(schemes []SignatureScheme) []SignatureScheme {
	if len(schemes) == 0 {
		return nil
	}
	out := make([]SignatureScheme, 0, len(schemes))
	for _, scheme := range schemes {
		if scheme.IsHandshakeSignatureScheme() {
			out = append(out, scheme)
		}
	}
	return out
}

// ContainsSignatureScheme reports whether scheme occurs in schemes.
func ContainsSignatureScheme(schemes []SignatureScheme, scheme SignatureScheme) bool {
	for _, candidate := range schemes {
		if candidate == scheme {
			return true
		}
	}
	return false
}

// SchemeInList is an argument-order convenience alias for
// ContainsSignatureScheme.
func SchemeInList(scheme SignatureScheme, schemes []SignatureScheme) bool {
	return ContainsSignatureScheme(schemes, scheme)
}

// ErrMalformedSignatureSchemeList is returned when an encoded
// SignatureSchemeList is not a sequence of 16-bit values.
var ErrMalformedSignatureSchemeList = errors.New("netsec: malformed signature scheme list")

// ParseSignatureSchemeList decodes the body of a signature_algorithms or
// signature_algorithms_cert extension.
func ParseSignatureSchemeList(data []byte) ([]SignatureScheme, error) {
	if len(data)%2 != 0 {
		return nil, ErrMalformedSignatureSchemeList
	}
	schemes := make([]SignatureScheme, 0, len(data)/2)
	for len(data) > 0 {
		schemes = append(schemes, SignatureScheme(binary.BigEndian.Uint16(data[:2])))
		data = data[2:]
	}
	return schemes, nil
}

// MarshalSignatureSchemeList encodes schemes as the body of a
// signature_algorithms or signature_algorithms_cert extension.
func MarshalSignatureSchemeList(schemes []SignatureScheme) []byte {
	out := make([]byte, 2*len(schemes))
	for i, scheme := range schemes {
		binary.BigEndian.PutUint16(out[2*i:], uint16(scheme))
	}
	return out
}

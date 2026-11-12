package netsec

import (
	"net/netip"
	"time"
)

// Key algorithm identifiers for the public key carried in a CSR.
const (
	KeyAlgorithmRSA     = "RSA"
	KeyAlgorithmECDSA   = "ECDSA"
	KeyAlgorithmEd25519 = "Ed25519"
)

// ECDSA curve identifiers.
const (
	CurveP256 = "P-256"
	CurveP384 = "P-384"
	CurveP521 = "P-521"
)

// X.509 extended key usages. A workload certificate typically carries one or
// both of client/server authentication; policy restricts the allowed set.
const (
	ExtKeyUsageServerAuth = "serverAuth"
	ExtKeyUsageClientAuth = "clientAuth"
)

// CSRContent is the security-relevant content parsed out of a PKCS#10 CSR.
type CSRContent struct {
	CommonName     string
	DNSNames       []string
	IPAddresses    []netip.Addr
	URIs           []string
	EmailAddresses []string
	KeyUsages      []string
	IsCA           bool
	KeyAlgorithm   string
	KeySize        int
	ECDSACurve     string
}

// Requester is the authenticated identity of the subject that created the
// certificate request.
type Requester struct {
	Username  string
	Groups    []string
	Namespace string
}

// CertificateDeclaration mirrors the SAN/key/basic-constraint fields declared
// on the Certificate resource. Every field must match the content of the
// embedded CSR; a mismatch means the request was tampered with after the
// resource was validated.
type CertificateDeclaration struct {
	CommonName     string
	DNSNames       []string
	IPAddresses    []netip.Addr
	URIs           []string
	EmailAddresses []string
	KeyUsages      []string
	IsCA           bool
	Duration       time.Duration
}

// Request is the full input handed to the signing policy.
type Request struct {
	Requester   Requester
	CSR         CSRContent
	Declaration CertificateDeclaration
}

// IssuerPolicy is the policy configured on an issuer. The per-namespace maps
// only apply to requests whose requester namespace is present as a key; a
// namespace without an entry receives no SAN grants.
type IssuerPolicy struct {
	// AllowedDomainSuffixes maps namespace -> permitted DNS suffixes
	// (e.g. "example.com"). A DNS name must end on a label boundary at one
	// of the suffixes, or equal it.
	AllowedDomainSuffixes map[string][]string

	// AllowedEmailDomains maps namespace -> permitted email address
	// domains, compared with the same label-boundary suffix rules.
	AllowedEmailDomains map[string][]string

	// AllowedIPPrefixes maps namespace -> CIDR prefixes that IP SANs may
	// fall within. A namespace without an entry may not request IP SANs.
	AllowedIPPrefixes map[string][]netip.Prefix

	// TrustDomains maps namespace -> SPIFFE trust domains (e.g.
	// "example.org"). URI SANs of form spiffe://<trust-domain>/... must
	// carry one of these trust domains.
	TrustDomains map[string][]string

	// AllowedURIPrefixes maps namespace -> allowed URI prefixes for
	// non-SPIFFE URI SANs (exact prefix match).
	AllowedURIPrefixes map[string][]string

	// ForbidCA rejects every request whose IsCA flag is set when true.
	ForbidCA bool

	// MaxDuration caps the declared validity period. Zero disables the
	// cap.
	MaxDuration time.Duration

	// MinRSAKeySize rejects RSA keys shorter than this. Zero defaults to
	// 2048.
	MinRSAKeySize int

	// AllowedECDSACurves lists the ECDSA curves that may be used.
	AllowedECDSACurves []string

	// AllowedKeyAlgorithms lists acceptable public-key algorithms. Empty
	// means RSA and ECDSA are allowed.
	AllowedKeyAlgorithms []string

	// AllowedKeyUsages restricts the extended key usages a request may
	// carry. Empty means no extra restriction beyond CSR consistency.
	AllowedKeyUsages []string
}

// Violation describes a single failed policy check.
type Violation struct {
	Code    string
	Message string
}

// Decision is the result of evaluating a request against an issuer policy.
type Decision struct {
	Allowed    bool
	Violations []Violation
}

// Error makes a rejected decision usable as an error.
func (d Decision) Error() string {
	if d.Allowed || len(d.Violations) == 0 {
		return ""
	}
	out := ""
	for i, v := range d.Violations {
		if i > 0 {
			out += "; "
		}
		out += v.Code + ": " + v.Message
	}
	return out
}

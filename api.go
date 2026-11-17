package netsec

import (
	"crypto/ed25519"
	"fmt"
	"time"
)

// Cert is an alias for Certificate.
type Cert = Certificate

// DelegationCertificate is an alias for Certificate.
type DelegationCertificate = Certificate

// Root is an alias for TrustAnchor.
type Root = TrustAnchor

// CertificateRevocation is an alias for Revocation.
type CertificateRevocation = Revocation

// VerificationResult is an alias for Result.
type VerificationResult = Result

// Chain is the leaf-to-root certificate chain returned by a successful
// verification.
type Chain = []*Certificate

// ParseCert is an alias for ParseCertificate.
func ParseCert(data []byte) (*Certificate, error) {
	return ParseCertificate(data)
}

// New is an alias for NewCertificate.
func New(
	subjectPublicKey ed25519.PublicKey,
	issuerID string,
	serial uint64,
	operations []string,
	resourcePrefixes []string,
	maxDelegationDepth uint32,
	notBefore, notAfter time.Time,
) *Certificate {
	return NewCertificate(subjectPublicKey, issuerID, serial, operations,
		resourcePrefixes, maxDelegationDepth, notBefore, notAfter)
}

// Sign signs cert with the issuer private key.
func Sign(cert *Certificate, privateKey ed25519.PrivateKey) error {
	return cert.Sign(privateKey)
}

// MarshalCertificate serializes a complete certificate, including signature.
func MarshalCertificate(cert Certificate) ([]byte, error) {
	return cert.MarshalBinary()
}

// VerifySignature verifies a certificate signature with an issuer key.
func VerifySignature(cert Certificate, issuerPublicKey ed25519.PublicKey) error {
	return cert.VerifySignature(issuerPublicKey)
}

// Validate checks the certificate's structural fields and signature length.
// It does not verify the signature against an issuer key.
func (c Certificate) Validate() error {
	if err := c.validateFields(); err != nil {
		return err
	}
	if len(c.Signature) != ed25519.SignatureSize {
		return fmt.Errorf("%w: signature has length %d, want %d",
			ErrInvalidCertificate, len(c.Signature), ed25519.SignatureSize)
	}
	return nil
}

// ValidateFields checks the certificate's structural fields without requiring
// a signature.
func (c Certificate) ValidateFields() error {
	return c.validateFields()
}

// NewRoot constructs a trust anchor.
func NewRoot(id string, publicKey ed25519.PublicKey) TrustAnchor {
	return TrustAnchor{ID: id, PublicKey: append(ed25519.PublicKey(nil), publicKey...)}
}

// Verify checks the certificate signature with the issuer public key.
func (c Certificate) Verify(issuerPublicKey []byte) error {
	return c.VerifySignature(issuerPublicKey)
}

// ValidAt reports whether the certificate validity interval contains t.
// The interval is [NotBefore, NotAfter).
func (c Certificate) ValidAt(t time.Time) bool {
	return !t.Before(c.NotBefore) && t.Before(c.NotAfter)
}

// IntersectOperations returns the sorted intersection of two operation sets.
func IntersectOperations(left, right []string) []string {
	leftNorm, errLeft := normalizeOperations(left)
	rightNorm, errRight := normalizeOperations(right)
	if errLeft != nil || errRight != nil {
		return nil
	}
	return intersectOperations(leftNorm, rightNorm)
}

// IntersectResourcePrefixes returns the exact component-wise intersection of
// two resource-prefix languages. The result is canonical and may be empty.
func IntersectResourcePrefixes(left, right []string) ([]string, error) {
	leftNorm, err := normalizePrefixSet(left)
	if err != nil {
		return nil, err
	}
	rightNorm, err := normalizePrefixSet(right)
	if err != nil {
		return nil, err
	}
	return prefixTexts(intersectPrefixes(reducePrefixes(leftNorm), reducePrefixes(rightNorm))), nil
}

// IntersectPrefixes is an alias for IntersectResourcePrefixes.
func IntersectPrefixes(left, right []string) ([]string, error) {
	return IntersectResourcePrefixes(left, right)
}

// ResourceAllowed reports whether any prefix authorizes the resource.
func ResourceAllowed(prefixes []string, resource string) (bool, error) {
	normalized, err := normalizePrefixSet(prefixes)
	if err != nil {
		return false, err
	}
	parsed, err := parsePath(resource)
	if err != nil {
		return false, err
	}
	return anyPrefixMatches(reducePrefixes(normalized), parsed), nil
}

// PrefixMatches is an alias for PathPrefixMatches.
func PrefixMatches(prefix, resource string) (bool, error) {
	return PathPrefixMatches(prefix, resource)
}

// Allows reports whether the effective policy supports the request.
func (p Policy) Allows(operation, resource string) bool {
	if !containsOperation(p.Operations, operation) {
		return false
	}
	parsed, err := parsePath(resource)
	if err != nil {
		return false
	}
	prefixes, err := normalizePrefixSet(p.ResourcePrefixes)
	if err != nil {
		return false
	}
	return anyPrefixMatches(reducePrefixes(prefixes), parsed)
}

// String returns a concise description of the policy for logs.
func (p Policy) String() string {
	return fmt.Sprintf("operations=%v prefixes=%v remainingDepth=%d",
		p.Operations, p.ResourcePrefixes, p.RemainingDelegationDepth)
}

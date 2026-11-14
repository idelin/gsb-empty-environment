// Package pinning implements certificate pinning for the mobile gateway
// TLS clients. Pins are declared per purpose and cover every certificate
// that may appear in a presented chain: leaf public keys and issuing CA
// SubjectPublicKeyInfo (SPKI) digests. Both the primary and the backup
// certificate chain must be declared; verification never falls back to
// the system trust store.
package pinning

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"strings"
)

// Purpose identifies a usage context (for example "gateway-api") that has
// its own declared pin set within a policy.
type Purpose string

// Digest is a SHA-256 digest of a certificate DER-encoded
// SubjectPublicKeyInfo.
type Digest [sha256.Size]byte

// SPKIDigest returns the SPKI SHA-256 digest of cert.
func SPKIDigest(cert *x509.Certificate) Digest {
	return Digest(sha256.Sum256(cert.RawSubjectPublicKeyInfo))
}

// ParseDigest parses a hex-encoded digest, optionally prefixed with
// "sha256/" or "sha256:".
func ParseDigest(s string) (Digest, error) {
	var d Digest
	s = strings.TrimPrefix(s, "sha256/")
	s = strings.TrimPrefix(s, "sha256:")
	raw, err := hex.DecodeString(s)
	if err != nil {
		return d, fmt.Errorf("pinning: invalid digest %q: %w", s, err)
	}
	if len(raw) != sha256.Size {
		return d, fmt.Errorf("pinning: digest %q has %d bytes, want %d", s, len(raw), sha256.Size)
	}
	copy(d[:], raw)
	return d, nil
}

func (d Digest) String() string { return "sha256/" + hex.EncodeToString(d[:]) }

// PinSet declares the certificates acceptable for one purpose. Every
// certificate in a presented chain is checked against the set: the leaf
// must match LeafKeys and every issuing certificate (intermediates and
// the root) must match IssuerKeys. A chain containing any certificate
// outside the set is rejected.
type PinSet struct {
	// LeafKeys holds SPKI digests of acceptable leaf public keys.
	LeafKeys map[Digest]struct{}
	// IssuerKeys holds SPKI digests of acceptable issuing CAs, covering
	// both intermediates and trust anchors of every declared chain
	// (primary and backup).
	IssuerKeys map[Digest]struct{}
}

// NewPinSet returns an empty pin set ready to be populated.
func NewPinSet() *PinSet {
	return &PinSet{
		LeafKeys:   make(map[Digest]struct{}),
		IssuerKeys: make(map[Digest]struct{}),
	}
}

// PinLeaf declares the public key of cert as an acceptable leaf key.
func (s *PinSet) PinLeaf(cert *x509.Certificate) { s.PinLeafDigest(SPKIDigest(cert)) }

// PinIssuer declares the public key of cert as an acceptable issuing CA key.
func (s *PinSet) PinIssuer(cert *x509.Certificate) { s.PinIssuerDigest(SPKIDigest(cert)) }

// PinLeafDigest declares an acceptable leaf SPKI digest.
func (s *PinSet) PinLeafDigest(d Digest) { s.LeafKeys[d] = struct{}{} }

// PinIssuerDigest declares an acceptable issuer SPKI digest.
func (s *PinSet) PinIssuerDigest(d Digest) { s.IssuerKeys[d] = struct{}{} }

// allowsLeaf reports whether the leaf certificate is pinned.
func (s *PinSet) allowsLeaf(cert *x509.Certificate) bool {
	_, ok := s.LeafKeys[SPKIDigest(cert)]
	return ok
}

// allowsIssuer reports whether the issuing certificate is pinned.
func (s *PinSet) allowsIssuer(cert *x509.Certificate) bool {
	_, ok := s.IssuerKeys[SPKIDigest(cert)]
	return ok
}

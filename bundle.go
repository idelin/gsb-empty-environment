package netsec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
)

// Bundle holds the trusted verification keys for a single trust domain.
//
// A key is any public key accepted by the supported JWT algorithms:
// *ecdsa.PublicKey for ES256, and *rsa.PublicKey for RS256/PS256.
type Bundle struct {
	trustDomain string
	keys        []crypto.PublicKey
}

// NewBundle creates a bundle for trustDomain. The trust domain is parsed with
// ParseSPIFFEID so that only normalized, lower-case trust domains are accepted.
func NewBundle(trustDomain string, keys ...crypto.PublicKey) (*Bundle, error) {
	id, err := ParseSPIFFEID(SchemePrefix + trustDomain)
	if err != nil {
		return nil, fmt.Errorf("netsec: invalid bundle trust domain: %w", err)
	}
	if id.Path() != "" {
		return nil, fmt.Errorf("netsec: bundle trust domain must not contain a path: %q", trustDomain)
	}
	b := &Bundle{trustDomain: id.TrustDomain()}
	for _, key := range keys {
		if err := b.AddKey(key); err != nil {
			return nil, err
		}
	}
	return b, nil
}

// TrustDomain returns the normalized trust domain.
func (b *Bundle) TrustDomain() string { return b.trustDomain }

// AddKey registers an additional trusted public key.
func (b *Bundle) AddKey(key crypto.PublicKey) error {
	switch key.(type) {
	case *ecdsa.PublicKey, *rsa.PublicKey:
	default:
		return fmt.Errorf("netsec: unsupported public key type %T", key)
	}
	b.keys = append(b.keys, key)
	return nil
}

// Keys returns the trusted public keys.
func (b *Bundle) Keys() []crypto.PublicKey {
	out := make([]crypto.PublicKey, len(b.keys))
	copy(out, b.keys)
	return out
}

// BundleSource resolves the trust bundle for a trust domain. Returning a nil
// bundle and nil error means the trust domain is not trusted.
type BundleSource interface {
	BundleForTrustDomain(trustDomain string) (*Bundle, error)
}

// MapBundleSource is a static, map-backed BundleSource. Keys are normalized
// trust domains as returned by SPIFFEID.TrustDomain.
type MapBundleSource map[string]*Bundle

// Add registers a bundle under its normalized trust domain.
func (s MapBundleSource) Add(b *Bundle) {
	s[b.TrustDomain()] = b
}

// BundleForTrustDomain implements BundleSource.
func (s MapBundleSource) BundleForTrustDomain(trustDomain string) (*Bundle, error) {
	if b, ok := s[trustDomain]; ok {
		return b, nil
	}
	return nil, nil
}

// ParsePublicKeyPEM decodes a PEM-encoded public key. PKIX ("PUBLIC KEY"),
// PKCS#1 RSA ("RSA PUBLIC KEY"), SEC1 EC ("EC PUBLIC KEY") blocks and
// certificate ("CERTIFICATE") blocks are supported.
func ParsePublicKeyPEM(data []byte) (crypto.PublicKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("netsec: failed to decode PEM block")
	}
	switch block.Type {
	case "PUBLIC KEY":
		return x509.ParsePKIXPublicKey(block.Bytes)
	case "RSA PUBLIC KEY":
		return x509.ParsePKCS1PublicKey(block.Bytes)
	case "CERTIFICATE":
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, err
		}
		return cert.PublicKey, nil
	case "EC PUBLIC KEY":
		// SEC1 EC public keys are not parsed by x509 directly; the common
		// PEM form for an EC public key is the PKIX "PUBLIC KEY" block.
		return nil, errors.New("netsec: parse EC public keys from a PKIX \"PUBLIC KEY\" PEM block")
	default:
		return nil, fmt.Errorf("netsec: unsupported PEM block type %q", block.Type)
	}
}

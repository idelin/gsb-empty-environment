package ctap2auth

import (
	"crypto/ecdh"
	"crypto/rand"
)

// p256PointSize is the fixed SEC1 uncompressed point length for P-256:
// 0x04 prefix + 32-byte X + 32-byte Y.
const p256PointSize = 65

// PlatformKey is the host/platform side of the ephemeral P-256 ECDH exchange.
// It wraps crypto/ecdh so that no third-party curve code is reachable.
type PlatformKey struct {
	priv *ecdh.PrivateKey
}

// GeneratePlatformKey creates a fresh P-256 ephemeral keypair.
func GeneratePlatformKey() (*PlatformKey, error) {
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &PlatformKey{priv: priv}, nil
}

// NewPlatformKeyFromBytes rebuilds a platform private key from a 32-byte scalar
// (used by tests and deterministic re-instantiation). crypto/ecdh rejects zero
// and out-of-range scalars.
func NewPlatformKeyFromBytes(scalar []byte) (*PlatformKey, error) {
	priv, err := ecdh.P256().NewPrivateKey(scalar)
	if err != nil {
		return nil, err
	}
	return &PlatformKey{priv: priv}, nil
}

// PublicKey returns the SEC1 uncompressed (65-byte) public point.
func (k *PlatformKey) PublicKey() []byte {
	return k.priv.PublicKey().Bytes()
}

// PrivateBytes returns the 32-byte private scalar.
func (k *PlatformKey) PrivateBytes() []byte {
	return k.priv.Bytes()
}

// SharedSecret performs ECDH after fully validating the peer's public point.
// crypto/ecdh.ECDH enforces the NIST SP 800-56A public-key validation on the
// P-256 curve, which rejects points not on the curve, the point at infinity and
// the small-order/low-order subgroup points, preventing invalid-curve and
// small-subgroup attacks.
func (k *PlatformKey) SharedSecret(peerPublic []byte) ([]byte, error) {
	if err := validatePointEncoding(peerPublic); err != nil {
		return nil, err
	}
	peer, err := ecdh.P256().NewPublicKey(peerPublic)
	if err != nil {
		// Any non-curve / low-order point lands here.
		return nil, ErrInvalidPoint
	}
	z, err := k.priv.ECDH(peer)
	if err != nil {
		return nil, ErrInvalidPoint
	}
	if len(z) != 32 {
		return nil, ErrInvalidSharedSecret
	}
	return z, nil
}

// ValidatePublicPoint exposes the hard point-validation gate to callers that
// only need to vet an authenticator keyAgreement key.
func ValidatePublicPoint(point []byte) error {
	if err := validatePointEncoding(point); err != nil {
		return err
	}
	if _, err := ecdh.P256().NewPublicKey(point); err != nil {
		return ErrInvalidPoint
	}
	return nil
}

// validatePointEncoding enforces the SEC1 uncompressed representation and
// rejects trivially malformed or all-zero encodings before curve validation.
func validatePointEncoding(point []byte) error {
	if len(point) != p256PointSize || point[0] != 0x04 {
		return ErrInvalidPoint
	}
	allZero := true
	for i := 1; i < p256PointSize; i++ {
		if point[i] != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		return ErrInvalidPoint
	}
	return nil
}

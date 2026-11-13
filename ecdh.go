package netsec

import (
	"crypto/ecdh"
	"crypto/rand"
)

// Curve returns the P-256 curve used by this package.
func Curve() ecdh.Curve { return ecdh.P256() }

// KeyPair is an ECDH private key together with its derived public key.
type KeyPair struct {
	private *ecdh.PrivateKey
}

// GenerateKey creates a fresh P-256 ECDH key pair.
func GenerateKey() (*KeyPair, error) {
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &KeyPair{private: priv}, nil
}

// PrivateKeyBytes returns the raw scalar of the private key.
func (k *KeyPair) PrivateKeyBytes() []byte {
	return k.private.Bytes()
}

// PublicKeyBytes returns the uncompressed (0x04 || x || y) encoding of the
// public key. The returned point is guaranteed to lie on the curve.
func (k *KeyPair) PublicKeyBytes() []byte {
	return k.private.PublicKey().Bytes()
}

// PublicKey exposes the underlying standard library public key.
func (k *KeyPair) PublicKey() *ecdh.PublicKey {
	return k.private.PublicKey()
}

// ValidatePeerPoint is the explicit gate every received public key must pass.
// It rejects points that have the right length but fail the curve equation.
func ValidatePeerPoint(peerPoint []byte) error {
	return ValidatePoint(peerPoint)
}

// SharedSecret computes the ECDH shared secret between the local private key
// and a peer's uncompressed public point. The peer point is first required to
// satisfy the P-256 curve equation; any malformed or off-curve point (including
// a length-correct point with a tampered y) makes the exchange fail.
func (k *KeyPair) SharedSecret(peerPoint []byte) ([]byte, error) {
	peer, err := parsePoint(peerPoint)
	if err != nil {
		return nil, err
	}
	return k.private.ECDH(peer)
}

// Exchange is a convenience wrapper around KeyPair.SharedSecret for callers
// that only need the resulting shared secret.
func Exchange(local *KeyPair, peerPoint []byte) ([]byte, error) {
	if local == nil {
		return nil, ErrInvalidPoint
	}
	return local.SharedSecret(peerPoint)
}

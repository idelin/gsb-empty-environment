package netsec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"fmt"
)

// trustedKey is a hop public key accepted by the verifier.
type trustedKey struct {
	hopID string
	alg   string
	key   crypto.PublicKey
}

// KeyRing is the verifier-side trust set: every (KeyID, HopID) pair maps to a
// hop public key and its signature algorithm.
type KeyRing struct {
	keys map[string]trustedKey
}

// NewKeyRing creates an empty trust set.
func NewKeyRing() *KeyRing {
	return &KeyRing{keys: make(map[string]trustedKey)}
}

// Add registers a hop public key under keyID. Re-registering an existing
// keyID with a different key or hop is rejected so trust entries cannot be
// silently replaced.
func (r *KeyRing) Add(hopID, keyID, alg string, key crypto.PublicKey) error {
	if r == nil {
		return fmt.Errorf("%w: nil key ring", ErrUnknownKey)
	}
	if hopID == "" || keyID == "" {
		return fmt.Errorf("%w: hop id and key id are required", ErrInvalidFact)
	}
	if key == nil {
		return fmt.Errorf("%w: nil public key", ErrInvalidFact)
	}
	if err := checkPublicKey(alg, key); err != nil {
		return err
	}
	if existing, ok := r.keys[keyID]; ok {
		if existing.hopID != hopID || existing.alg != alg {
			return fmt.Errorf("%w: key id %q already registered", ErrInvalidFact, keyID)
		}
	}
	r.keys[keyID] = trustedKey{hopID: hopID, alg: alg, key: key}
	return nil
}

func checkPublicKey(alg string, key crypto.PublicKey) error {
	switch alg {
	case AlgECDSAP256SHA256:
		pub, ok := key.(*ecdsa.PublicKey)
		if !ok {
			return fmt.Errorf("%w: %s requires *ecdsa.PublicKey", ErrUnsupportedAlg, alg)
		}
		if pub.Params().Name != "P-256" {
			return fmt.Errorf("%w: %s requires P-256 curve", ErrUnsupportedAlg, alg)
		}
	case AlgEd25519:
		if _, ok := key.(ed25519.PublicKey); !ok {
			return fmt.Errorf("%w: %s requires ed25519.PublicKey", ErrUnsupportedAlg, alg)
		}
	case AlgRSAPSSSHA256:
		if _, ok := key.(*rsa.PublicKey); !ok {
			return fmt.Errorf("%w: %s requires *rsa.PublicKey", ErrUnsupportedAlg, alg)
		}
	default:
		return fmt.Errorf("%w: %q", ErrUnsupportedAlg, alg)
	}
	return nil
}

func (r *KeyRing) lookup(keyID string) (trustedKey, error) {
	entry, ok := r.keys[keyID]
	if !ok {
		return trustedKey{}, fmt.Errorf("%w: %q", ErrUnknownKey, keyID)
	}
	return entry, nil
}

// verifySignature checks a.Signature against a.canonicalBytes() using the
// key and algorithm recorded in the trust set. A mismatch between the
// attestation's SigAlg and the trusted algorithm is rejected.
func verifySignature(a *Attestation, entry trustedKey) error {
	if a.SigAlg != entry.alg {
		return fmt.Errorf("%w: attestation uses %q, trusted key uses %q",
			ErrBadSignature, a.SigAlg, entry.alg)
	}
	payload := a.canonicalBytes()
	switch entry.alg {
	case AlgECDSAP256SHA256:
		digest := sha256.Sum256(payload)
		pub := entry.key.(*ecdsa.PublicKey)
		if !ecdsa.VerifyASN1(pub, digest[:], a.Signature) {
			return ErrBadSignature
		}
	case AlgEd25519:
		pub := entry.key.(ed25519.PublicKey)
		if !ed25519.Verify(pub, payload, a.Signature) {
			return ErrBadSignature
		}
	case AlgRSAPSSSHA256:
		digest := sha256.Sum256(payload)
		pub := entry.key.(*rsa.PublicKey)
		if err := rsa.VerifyPSS(pub, crypto.SHA256, digest[:], a.Signature, &rsa.PSSOptions{
			SaltLength: rsa.PSSSaltLengthEqualsHash,
		}); err != nil {
			return fmt.Errorf("%w: %v", ErrBadSignature, err)
		}
	default:
		return fmt.Errorf("%w: %q", ErrUnsupportedAlg, entry.alg)
	}
	return nil
}

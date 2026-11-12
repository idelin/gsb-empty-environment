package netsec

import (
	"context"
	"crypto/hmac"
	"crypto/subtle"
)

// PseudoSigner is a deterministic HMAC-SHA256 signer intended for tests and
// local integration. It is not a public-key signature scheme and must not be
// used as the trust anchor in production; hosts should supply their own
// Verifier backed by device/user key material.

// Signer produces a signature over a proof's canonical content.
type Signer interface {
	Sign(p *Proof) error
}

type PseudoSigner struct {
	key []byte
}

// NewPseudoSigner builds a signer from a shared key.
func NewPseudoSigner(key []byte) *PseudoSigner {
	return &PseudoSigner{key: append([]byte(nil), key...)}
}

// Sign fills p.Sig with the HMAC over p's canonical bytes. The caller owns
// the other fields and should set SignerID before signing.
func (s *PseudoSigner) Sign(p *Proof) error {
	raw, err := p.canonicalBytes()
	if err != nil {
		return err
	}
	mac := hmac.New(hmacHash, s.key)
	mac.Write(raw)
	p.Sig = mac.Sum(nil)
	return nil
}

// PseudoVerifier checks proofs produced by a PseudoSigner holding the same key.
type PseudoVerifier struct {
	key []byte
}

// NewPseudoVerifier builds a verifier from a shared key.
func NewPseudoVerifier(key []byte) *PseudoVerifier {
	return &PseudoVerifier{key: append([]byte(nil), key...)}
}

// VerifyProof implements Verifier using constant-time HMAC comparison.
func (v *PseudoVerifier) VerifyProof(_ context.Context, p Proof) (bool, error) {
	raw, err := p.canonicalBytes()
	if err != nil {
		return false, err
	}
	mac := hmac.New(hmacHash, v.key)
	mac.Write(raw)
	expected := mac.Sum(nil)
	return subtle.ConstantTimeCompare(expected, p.Sig) == 1, nil
}

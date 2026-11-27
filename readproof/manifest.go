package readproof

import (
	"crypto/ed25519"
)

// Manifest is a signed, versioned file commitment. It binds the file
// identity to the file length, a monotonically increasing version, the
// previous-version root and the current-version root.
type Manifest struct {
	FileID       string
	Length       uint64
	Version      uint64
	PreviousRoot [32]byte
	Root         [32]byte
	Signature    []byte
}

// Signer signs canonical manifest bytes.
type Signer interface {
	Sign(message []byte) ([]byte, error)
}

// Verifier verifies manifest signatures.
type Verifier interface {
	Verify(message, signature []byte) bool
}

// Ed25519Signer adapts an ed25519 private key to Signer.
type Ed25519Signer ed25519.PrivateKey

// Sign implements Signer.
func (k Ed25519Signer) Sign(message []byte) ([]byte, error) {
	return ed25519.Sign(ed25519.PrivateKey(k), message), nil
}

// Ed25519Verifier adapts an ed25519 public key to Verifier.
type Ed25519Verifier ed25519.PublicKey

// Verify implements Verifier.
func (k Ed25519Verifier) Verify(message, signature []byte) bool {
	return ed25519.Verify(ed25519.PublicKey(k), message, signature)
}

// signingBytes returns the canonical, domain-separated encoding that is
// signed. Every variable-length field is length-prefixed and every integer
// is fixed-width, so distinct manifests have distinct encodings.
func (m *Manifest) signingBytes() []byte {
	b := appendString(nil, manifestDomain)
	b = appendString(b, []byte(m.FileID))
	b = appendUint64(b, m.Length)
	b = appendUint64(b, m.Version)
	b = append(b, m.PreviousRoot[:]...)
	b = append(b, m.Root[:]...)
	return b
}

// Sign fills Signature using signer.
func (m *Manifest) Sign(signer Signer) error {
	sig, err := signer.Sign(m.signingBytes())
	if err != nil {
		return err
	}
	m.Signature = sig
	return nil
}

// Verify checks the signature using verifier.
func (m *Manifest) Verify(verifier Verifier) error {
	if !verifier.Verify(m.signingBytes(), m.Signature) {
		return ErrBadSignature
	}
	return nil
}

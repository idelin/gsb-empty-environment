package netsec

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
)

// Signer is the migration host signing identity.  Only the target's
// provisioned host public key is trusted for startup.
type Signer struct {
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
}

// NewSigner constructs a signer from a 32-byte Ed25519 seed or a 64-byte
// private key.  A nil seed generates a fresh key.
func NewSigner(seed []byte) (*Signer, error) {
	if seed == nil {
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, fmt.Errorf("netsec: generate host key: %w", err)
		}
		return signerFromPrivate(priv), nil
	}
	if len(seed) == ed25519.PrivateKeySize {
		return NewSignerFromPrivateKey(ed25519.PrivateKey(append([]byte(nil), seed...)))
	}
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("netsec: Ed25519 seed must be %d bytes or private key %d bytes, got %d",
			ed25519.SeedSize, ed25519.PrivateKeySize, len(seed))
	}
	return signerFromPrivate(ed25519.NewKeyFromSeed(append([]byte(nil), seed...))), nil
}

// NewRandomSigner generates a fresh host signing key.
func NewRandomSigner() (*Signer, error) {
	return NewSigner(nil)
}

// NewSignerFromPrivateKey wraps an existing host key.
func NewSignerFromPrivateKey(priv ed25519.PrivateKey) (*Signer, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("netsec: invalid Ed25519 private key length %d", len(priv))
	}
	return signerFromPrivate(append(ed25519.PrivateKey(nil), priv...)), nil
}

func signerFromPrivate(priv ed25519.PrivateKey) *Signer {
	pub := append(ed25519.PublicKey(nil), priv.Public().(ed25519.PublicKey)...)
	return &Signer{priv: priv, pub: pub}
}

// PublicKey returns a copy of the host public key.
func (s *Signer) PublicKey() ed25519.PublicKey {
	if s == nil {
		return nil
	}
	return append(ed25519.PublicKey(nil), s.pub...)
}

// Public is an alias for PublicKey.
func (s *Signer) Public() ed25519.PublicKey {
	return s.PublicKey()
}

// BuildReceipt fills a receipt from a validated final manifest.
func (s *Signer) BuildReceipt(m Manifest) (Receipt, error) {
	if err := m.Validate(); err != nil {
		return Receipt{}, err
	}
	if s == nil {
		return Receipt{}, fmt.Errorf("%w: nil signer", ErrBadSignature)
	}
	r := Receipt{
		VM:             m.VM,
		SourceInstance: m.SourceInstance,
		TargetInstance: m.TargetInstance,
		FinalEpoch:     m.Epoch,
		Pages:          m.PageGenerations(),
		DeviceDigest:   m.DeviceDigest,
		ManifestRoot:   m.Root(),
	}
	r.Signature = ed25519.Sign(s.priv, r.Payload())
	return r, nil
}

// VerifyReceipt verifies the host signature and checks that the receipt is
// internally bound to the supplied manifest.
func VerifyReceipt(pub ed25519.PublicKey, r Receipt, m Manifest) error {
	if len(pub) != ed25519.PublicKeySize {
		return ErrUntrustedHost
	}
	if len(r.Signature) != ed25519.SignatureSize {
		return fmt.Errorf("%w: signature length %d", ErrBadSignature, len(r.Signature))
	}
	if !ed25519.Verify(pub, r.Payload(), r.Signature) {
		return ErrBadSignature
	}
	if err := m.Validate(); err != nil {
		return err
	}
	if r.VM != m.VM ||
		r.SourceInstance != m.SourceInstance ||
		r.TargetInstance != m.TargetInstance ||
		r.FinalEpoch != m.Epoch ||
		r.Pages != m.PageGenerations() ||
		r.DeviceDigest != m.DeviceDigest ||
		r.ManifestRoot != m.Root() {
		return fmt.Errorf("%w: receipt does not bind the supplied manifest", ErrInvalidReceipt)
	}
	return nil
}

// Verify is a convenience method for VerifyReceipt.
func (r Receipt) Verify(pub ed25519.PublicKey, m Manifest) error {
	return VerifyReceipt(pub, r, m)
}

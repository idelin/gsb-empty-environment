package netsec

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
)

// algEd25519 identifies Ed25519 signatures inside a signature blob.
const algEd25519 = 0x01

// Ed25519Key is a trusted Ed25519 public key.
type Ed25519Key struct {
	key  ed25519.PublicKey
	hash uint32
	name string
}

// Name returns the verifier name.
func (k *Ed25519Key) Name() string { return k.name }

// KeyHash returns SHA-256(name || "\n" || 0x01 || key) truncated to 32 bits.
func (k *Ed25519Key) KeyHash() uint32 { return k.hash }

// Verify checks sig against msg. The algorithm byte must select Ed25519.
func (k *Ed25519Key) Verify(msg, sig []byte) bool {
	if len(sig) != 1+ed25519.SignatureSize || sig[0] != algEd25519 {
		return false
	}
	return ed25519.Verify(k.key, msg, sig[1:])
}

// Ed25519Signer holds an Ed25519 private key and produces note signatures.
type Ed25519Signer struct {
	Ed25519Key
	priv ed25519.PrivateKey
}

// Sign returns the algorithm byte followed by the Ed25519 signature of msg.
func (s *Ed25519Signer) Sign(msg []byte) ([]byte, error) {
	sig := ed25519.Sign(s.priv, msg)
	return append([]byte{algEd25519}, sig...), nil
}

// NewVerifier wraps an Ed25519 public key under the given verifier name.
func NewVerifier(name string, key ed25519.PublicKey) (*Ed25519Key, error) {
	if !validName(name) {
		return nil, fmt.Errorf("netsec: invalid verifier name %q", name)
	}
	if len(key) != ed25519.PublicKeySize {
		return nil, errors.New("netsec: invalid Ed25519 public key")
	}
	return &Ed25519Key{key: key, hash: keyHash(name, key), name: name}, nil
}

// NewSigner wraps an Ed25519 private key under the given verifier name.
func NewSigner(name string, priv ed25519.PrivateKey) (*Ed25519Signer, error) {
	if !validName(name) {
		return nil, fmt.Errorf("netsec: invalid signer name %q", name)
	}
	if len(priv) != ed25519.PrivateKeySize {
		return nil, errors.New("netsec: invalid Ed25519 private key")
	}
	pub := priv.Public().(ed25519.PublicKey)
	return &Ed25519Signer{
		Ed25519Key: Ed25519Key{key: pub, hash: keyHash(name, pub), name: name},
		priv:       priv,
	}, nil
}

// GenerateKey creates a fresh named Ed25519 signer.
func GenerateKey(name string) (*Ed25519Signer, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return NewSigner(name, priv)
}

// Ed25519Verifiers is a set of trusted Ed25519 keys with a signature
// threshold that a note must reach.
type Ed25519Verifiers struct {
	Keys      []*Ed25519Key
	Threshold int
}

// Verifiers returns the trusted keys and the required distinct-signer count.
// A zero or negative threshold defaults to one.
func (vs *Ed25519Verifiers) Verifiers() ([]Verifier, int) {
	threshold := vs.Threshold
	if threshold < 1 {
		threshold = 1
	}
	out := make([]Verifier, 0, len(vs.Keys))
	for _, key := range vs.Keys {
		out = append(out, key)
	}
	return out, threshold
}

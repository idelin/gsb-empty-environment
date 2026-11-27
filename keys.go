package noise

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/subtle"
	"fmt"
	"io"
)

const (
	keySize   = 32
	hashSize  = 32
	tagSize   = 16
	nonceSize = 12
)

// KeyPair is an X25519 private/public key pair. Private key material is kept
// unexported and is cleared by Destroy.
type KeyPair struct {
	private *ecdh.PrivateKey
	raw     [keySize]byte
	public  [keySize]byte
}

// NewKeyPair creates a key pair from 32 bytes of X25519 private key material.
// The input is copied.
func NewKeyPair(privateKey []byte) (*KeyPair, error) {
	if len(privateKey) != keySize {
		return nil, fmt.Errorf("%w: private key must be %d bytes", ErrInvalidKey, keySize)
	}
	priv, err := ecdh.X25519().NewPrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidKey, err)
	}
	kp := &KeyPair{private: priv}
	copy(kp.raw[:], privateKey)
	copy(kp.public[:], priv.PublicKey().Bytes())
	return kp, nil
}

// GenerateKeyPair generates a random X25519 key pair from random.
func GenerateKeyPair(random io.Reader) (*KeyPair, error) {
	if random == nil {
		random = rand.Reader
	}
	var raw [keySize]byte
	if _, err := io.ReadFull(random, raw[:]); err != nil {
		return nil, fmt.Errorf("noise: generate X25519 key: %w", err)
	}
	kp, err := NewKeyPair(raw[:])
	zero(raw[:])
	return kp, err
}

// PublicKey returns a copy of the 32-byte X25519 public key.
func (k *KeyPair) PublicKey() []byte {
	if k == nil || !k.usable() {
		return nil
	}
	out := make([]byte, keySize)
	copy(out, k.public[:])
	return out
}

// PrivateKey returns a copy of the raw 32-byte X25519 private key. Callers
// should clear the returned slice after use.
func (k *KeyPair) PrivateKey() []byte {
	if k == nil || !k.usable() {
		return nil
	}
	out := make([]byte, keySize)
	copy(out, k.raw[:])
	return out
}

// Clone returns a copy of the key pair.
func (k *KeyPair) Clone() (*KeyPair, error) {
	if k == nil || !k.usable() {
		return nil, fmt.Errorf("%w: unusable key pair", ErrInvalidKey)
	}
	return NewKeyPair(k.raw[:])
}

// Destroy clears the private-key bytes held by the receiver.
func (k *KeyPair) Destroy() {
	if k == nil {
		return
	}
	zero(k.raw[:])
	zero(k.public[:])
	k.private = nil
}

func (k *KeyPair) usable() bool {
	return k != nil && k.private != nil
}

// publicKey is a parsed remote X25519 public key.
type publicKey struct {
	key *ecdh.PublicKey
	raw [keySize]byte
}

func parsePublicKey(raw []byte) (*publicKey, error) {
	if len(raw) != keySize {
		return nil, fmt.Errorf("%w: public key must be %d bytes", ErrInvalidKey, keySize)
	}
	key, err := ecdh.X25519().NewPublicKey(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidKey, err)
	}
	pk := &publicKey{key: key}
	copy(pk.raw[:], raw)
	return pk, nil
}

func (p *publicKey) bytes() []byte {
	if p == nil {
		return nil
	}
	out := make([]byte, keySize)
	copy(out, p.raw[:])
	return out
}

func equalKey(a, b []byte) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare(a, b) == 1
}

// dh performs X25519 and rejects both ECDH errors and all-zero outputs. The
// explicit all-zero check is required by Noise revision 34 and protects
// against low-order-point attacks.
func dh(local *KeyPair, remote *publicKey) ([keySize]byte, error) {
	var out [keySize]byte
	if !local.usable() || remote == nil || remote.key == nil {
		return out, ErrInvalidKey
	}
	shared, err := local.private.ECDH(remote.key)
	if err != nil {
		return out, ErrLowOrderPoint
	}
	if len(shared) != keySize {
		zero(shared)
		return out, ErrLowOrderPoint
	}
	if isZero(shared) {
		zero(shared)
		return out, ErrLowOrderPoint
	}
	copy(out[:], shared)
	zero(shared)
	return out, nil
}

func isZero(b []byte) bool {
	var v byte
	for _, x := range b {
		v |= x
	}
	return v == 0
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

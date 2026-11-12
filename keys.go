package netsec

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
)

// StaticKeyPair is an X25519 key pair used as the Noise static key.
type StaticKeyPair struct {
	PublicKey  []byte
	privateKey []byte
}

// GenerateStaticKeyPair creates a fresh X25519 static key pair.
func GenerateStaticKeyPair() (*StaticKeyPair, error) {
	return generateStaticKeyPair(rand.Reader)
}

func generateStaticKeyPair(random io.Reader) (*StaticKeyPair, error) {
	key, err := ecdh.X25519().GenerateKey(random)
	if err != nil {
		return nil, fmt.Errorf("netsec: generate static key: %w", err)
	}
	return staticKeyPairFromBytes(key.Bytes())
}

// NewStaticKeyPair reconstructs an X25519 static key pair from its private
// key bytes.
func NewStaticKeyPair(privateKey []byte) (*StaticKeyPair, error) {
	return staticKeyPairFromBytes(privateKey)
}

func staticKeyPairFromBytes(privateKey []byte) (*StaticKeyPair, error) {
	if len(privateKey) != StaticKeySize {
		return nil, fmt.Errorf("%w: X25519 private key has length %d", ErrInvalidKey, len(privateKey))
	}
	key, err := ecdh.X25519().NewPrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidKey, err)
	}
	return &StaticKeyPair{
		PublicKey:  append([]byte(nil), key.PublicKey().Bytes()...),
		privateKey: append([]byte(nil), privateKey...),
	}, nil
}

// PrivateKeyBytes returns a copy of the private key bytes.
func (k *StaticKeyPair) PrivateKeyBytes() []byte {
	if k == nil {
		return nil
	}
	return append([]byte(nil), k.privateKey...)
}

func (k *StaticKeyPair) clone() *StaticKeyPair {
	if k == nil {
		return nil
	}
	return &StaticKeyPair{
		PublicKey:  append([]byte(nil), k.PublicKey...),
		privateKey: append([]byte(nil), k.privateKey...),
	}
}

func (k *StaticKeyPair) private() (*ecdh.PrivateKey, error) {
	if k == nil || len(k.privateKey) != StaticKeySize {
		return nil, ErrInvalidKey
	}
	return ecdh.X25519().NewPrivateKey(k.privateKey)
}

func (k *StaticKeyPair) public() (*ecdh.PublicKey, error) {
	if k == nil || len(k.PublicKey) != StaticKeySize {
		return nil, ErrInvalidKey
	}
	return ecdh.X25519().NewPublicKey(k.PublicKey)
}

func x25519(priv *ecdh.PrivateKey, peerPublic []byte) ([]byte, error) {
	if priv == nil {
		return nil, ErrInvalidKey
	}
	publicKey, err := ecdh.X25519().NewPublicKey(peerPublic)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidKey, err)
	}
	shared, err := priv.ECDH(publicKey)
	if err != nil {
		return nil, fmt.Errorf("netsec: X25519 exchange: %w", err)
	}
	return shared, nil
}

// IdentityKeyPair is an Ed25519 key pair used to authenticate the Noise
// static key.
type IdentityKeyPair struct {
	PublicKey  ed25519.PublicKey
	PrivateKey ed25519.PrivateKey
}

// GenerateIdentityKeyPair creates a fresh Ed25519 identity key pair.
func GenerateIdentityKeyPair() (*IdentityKeyPair, error) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("netsec: generate identity key: %w", err)
	}
	return &IdentityKeyPair{
		PublicKey:  append(ed25519.PublicKey(nil), publicKey...),
		PrivateKey: append(ed25519.PrivateKey(nil), privateKey...),
	}, nil
}

// NewIdentityKeyPair reconstructs an Ed25519 key pair from its private key.
func NewIdentityKeyPair(privateKey ed25519.PrivateKey) (*IdentityKeyPair, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%w: Ed25519 private key has length %d", ErrInvalidKey, len(privateKey))
	}
	privateCopy := append(ed25519.PrivateKey(nil), privateKey...)
	publicKey := append(ed25519.PublicKey(nil), privateCopy[32:]...)
	return &IdentityKeyPair{PublicKey: publicKey, PrivateKey: privateCopy}, nil
}

// Fingerprint returns a short, human-readable key fingerprint for logging.
func (k *IdentityKeyPair) Fingerprint() string {
	if k == nil || len(k.PublicKey) == 0 {
		return ""
	}
	return hex.EncodeToString(k.PublicKey)
}

package netsec

import (
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"fmt"
)

// LevelVerifier authenticates the signed security level of a version node.
type LevelVerifier interface {
	VerifyLevel(nodeID string, level SecurityLevel, signature []byte) error
}

// LevelSigner signs the security level that a manifest publisher attaches to
// a node. It is the counterpart of LevelVerifier and is intended for manifest
// tooling and tests rather than the update client itself.
type LevelSigner interface {
	SignLevel(nodeID string, level SecurityLevel) ([]byte, error)
}

// levelMessage is the deterministic byte string covered by every signature.
// Prefixing and fixed-width little-endian encoding remove ambiguity between
// different node IDs and levels.
func levelMessage(nodeID string, level SecurityLevel) []byte {
	msg := make([]byte, 0, 16+len(nodeID))
	msg = append(msg, "netsec-level-v1:"...)
	msg = append(msg, nodeID...)
	msg = append(msg, byte(':'))
	var levelBytes [8]byte
	binary.LittleEndian.PutUint64(levelBytes[:], uint64(level))
	msg = append(msg, levelBytes[:]...)
	return msg
}

// Ed25519Verifier checks level signatures against one trusted public key.
type Ed25519Verifier struct {
	PublicKey ed25519.PublicKey
}

// NewEd25519Verifier constructs a verifier, rejecting keys of the wrong size.
func NewEd25519Verifier(publicKey ed25519.PublicKey) (*Ed25519Verifier, error) {
	if len(publicKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("ed25519 public key must be %d bytes, got %d",
			ed25519.PublicKeySize, len(publicKey))
	}
	return &Ed25519Verifier{PublicKey: publicKey}, nil
}

// VerifyLevel implements LevelVerifier.
func (v *Ed25519Verifier) VerifyLevel(nodeID string, level SecurityLevel, signature []byte) error {
	if v == nil || len(v.PublicKey) != ed25519.PublicKeySize {
		return errors.New("ed25519 verifier is not initialized with a valid public key")
	}
	if len(signature) != ed25519.SignatureSize {
		return fmt.Errorf("signature must be %d bytes, got %d",
			ed25519.SignatureSize, len(signature))
	}
	if !ed25519.Verify(v.PublicKey, levelMessage(nodeID, level), signature) {
		return errors.New("ed25519 signature does not match node ID and security level")
	}
	return nil
}

// Ed25519Signer produces level signatures with the publisher's private key.
type Ed25519Signer struct {
	PrivateKey ed25519.PrivateKey
}

// NewEd25519Signer constructs a signer, rejecting keys of the wrong size.
func NewEd25519Signer(privateKey ed25519.PrivateKey) (*Ed25519Signer, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("ed25519 private key must be %d bytes, got %d",
			ed25519.PrivateKeySize, len(privateKey))
	}
	return &Ed25519Signer{PrivateKey: privateKey}, nil
}

// SignLevel implements LevelSigner.
func (s *Ed25519Signer) SignLevel(nodeID string, level SecurityLevel) ([]byte, error) {
	if s == nil || len(s.PrivateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("ed25519 signer is not initialized with a valid private key")
	}
	return ed25519.Sign(s.PrivateKey, levelMessage(nodeID, level)), nil
}

// GenerateLevelKey creates a fresh Ed25519 key pair for signing level claims.
func GenerateLevelKey() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	return ed25519.GenerateKey(nil)
}

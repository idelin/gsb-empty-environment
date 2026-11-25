package replayhandoff

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
)

// NodeSigner owns a node's Ed25519 signing key.
type NodeSigner struct {
	Node    NodeID
	Private ed25519.PrivateKey
	Public  ed25519.PublicKey
}

// NewNodeSigner wraps an existing Ed25519 private key.
func NewNodeSigner(node NodeID, private ed25519.PrivateKey) (*NodeSigner, error) {
	if node == "" {
		return nil, errors.New("replayhandoff: signer node must not be empty")
	}
	if len(private) != ed25519.PrivateKeySize {
		return nil, errors.New("replayhandoff: invalid Ed25519 private key")
	}
	pub, ok := private.Public().(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("replayhandoff: private key does not expose an Ed25519 public key")
	}
	return &NodeSigner{Node: node, Private: private, Public: pub}, nil
}

// NewSeedSigner is a deterministic deployment/test helper. Each node must use
// a unique seed.
func NewSeedSigner(node NodeID, seed [ed25519.SeedSize]byte) (*NodeSigner, error) {
	return NewNodeSigner(node, ed25519.NewKeyFromSeed(seed[:]))
}

// GenerateNodeSigner creates a fresh node key.
func GenerateNodeSigner(node NodeID) (*NodeSigner, KeyRing, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	s, err := NewNodeSigner(node, priv)
	if err != nil {
		return nil, nil, err
	}
	kr := KeyRing{node: pub}
	return s, kr, nil
}

// Sign signs message with the node key.
func (s *NodeSigner) Sign(message []byte) []byte {
	if s == nil || len(s.Private) == 0 {
		return nil
	}
	return ed25519.Sign(s.Private, message)
}

// KeyRing is the trusted public-key set for nodes.
type KeyRing map[NodeID]ed25519.PublicKey

// Clone returns a defensive copy of the key ring.
func (kr KeyRing) Clone() KeyRing {
	out := make(KeyRing, len(kr))
	for node, pub := range kr {
		out[node] = append(ed25519.PublicKey(nil), pub...)
	}
	return out
}

// Add inserts a public key.
func (kr KeyRing) Add(node NodeID, public ed25519.PublicKey) KeyRing {
	kr[node] = append(ed25519.PublicKey(nil), public...)
	return kr
}

// Verify checks a node signature over message.
func (kr KeyRing) Verify(node NodeID, message, signature []byte) error {
	public, ok := kr[node]
	if !ok {
		return fmt.Errorf("replayhandoff: no trusted key for node %q", node)
	}
	if len(public) != ed25519.PublicKeySize {
		return fmt.Errorf("replayhandoff: invalid public key for node %q", node)
	}
	if len(signature) != ed25519.SignatureSize {
		return fmt.Errorf("replayhandoff: invalid signature length for node %q", node)
	}
	if !ed25519.Verify(public, message, signature) {
		return fmt.Errorf("replayhandoff: signature verification failed for node %q", node)
	}
	return nil
}

// SigningMessage prefixes a domain separator and hashes a payload before
// signing. It avoids ambiguous use of the raw node key across artifacts.
func SigningMessage(domain string, payload []byte) []byte {
	h := sha256.New()
	writeLengthPrefixed(h, []byte("replayhandoff/sign/v1"))
	writeLengthPrefixed(h, []byte(domain))
	writeLengthPrefixed(h, payload)
	return h.Sum(nil)
}

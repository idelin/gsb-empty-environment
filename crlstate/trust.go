package crlstate

import (
	"crypto/ed25519"
	"fmt"
	"sync"
)

// Verifier authenticates a manifest or migration signature. issuer is supplied
// for audit and policy hooks; root identifies the signing root.
type Verifier interface {
	Verify(root RootID, issuer IssuerID, digest [32]byte, signature []byte) bool
}

// Ed25519Roots is a simple root registry keyed by RootID.
type Ed25519Roots struct {
	mu   sync.RWMutex
	keys map[RootID]ed25519.PublicKey
}

// NewEd25519Roots returns an empty root registry.
func NewEd25519Roots() *Ed25519Roots {
	return &Ed25519Roots{keys: make(map[RootID]ed25519.PublicKey)}
}

// AddRoot registers a public key. Re-registering the same key is idempotent;
// reusing a RootID with a different key is rejected.
func (r *Ed25519Roots) AddRoot(id RootID, publicKey ed25519.PublicKey) error {
	if id == "" {
		return fmt.Errorf("crlstate: empty root id")
	}
	if len(publicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("crlstate: root %q has invalid ed25519 public key length %d", id, len(publicKey))
	}
	key := append(ed25519.PublicKey(nil), publicKey...)
	r.mu.Lock()
	defer r.mu.Unlock()
	if old, ok := r.keys[id]; ok {
		if !old.Equal(key) {
			return fmt.Errorf("crlstate: root id %q already bound to a different key", id)
		}
		return nil
	}
	r.keys[id] = key
	return nil
}

// Verify implements Verifier.
func (r *Ed25519Roots) Verify(root RootID, _ IssuerID, digest [32]byte, signature []byte) bool {
	r.mu.RLock()
	key, ok := r.keys[root]
	r.mu.RUnlock()
	if !ok || len(signature) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(key, digest[:], signature)
}

// SignManifest signs m in place with root's ed25519 private key.
func SignManifest(m *Manifest, _ RootID, privateKey ed25519.PrivateKey) error {
	if m == nil {
		return fmt.Errorf("crlstate: nil manifest")
	}
	if len(privateKey) != ed25519.PrivateKeySize {
		return fmt.Errorf("crlstate: invalid ed25519 private key length %d", len(privateKey))
	}
	// Sign the same normalized representation that AddManifest verifies.
	normalizeManifest(m)
	d := m.SigningDigest()
	m.Signature = ed25519.Sign(privateKey, d[:])
	return nil
}

// SignMigration signs mig with both the old and new root keys.
func SignMigration(mig *Migration, oldKey, newKey ed25519.PrivateKey) error {
	if mig == nil {
		return fmt.Errorf("crlstate: nil migration")
	}
	if len(oldKey) != ed25519.PrivateKeySize || len(newKey) != ed25519.PrivateKeySize {
		return fmt.Errorf("crlstate: invalid ed25519 private key")
	}
	d := mig.SigningDigest()
	mig.OldSignature = ed25519.Sign(oldKey, d[:])
	mig.NewSignature = ed25519.Sign(newKey, d[:])
	return nil
}

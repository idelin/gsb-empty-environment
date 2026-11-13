package netsec

import (
	"crypto/ed25519"
	"crypto/sha256"
	"testing"
)

func testRegistry(t *testing.T) *Registry {
	t.Helper()
	return NewRegistry()
}

func testParty(t *testing.T, registry *Registry, name string) *Party {
	t.Helper()
	seed := sha256.Sum256([]byte("example.org/netsec/q0075/test/" + name))
	private := ed25519.NewKeyFromSeed(seed[:])
	public := private.Public().(ed25519.PublicKey)
	if err := registry.Register(name, public); err != nil {
		t.Fatalf("register %s: %v", name, err)
	}
	party, err := NewParty(name, private, registry)
	if err != nil {
		t.Fatalf("new party %s: %v", name, err)
	}
	return party
}

func testParties(t *testing.T, names ...string) (*Registry, map[string]*Party) {
	t.Helper()
	registry := testRegistry(t)
	parties := make(map[string]*Party, len(names))
	for _, name := range names {
		parties[name] = testParty(t, registry, name)
	}
	return registry, parties
}

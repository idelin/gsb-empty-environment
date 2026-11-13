package netsec

import (
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"testing"
)

func TestRegistryRejectsInvalidNamesAndKeys(t *testing.T) {
	registry := NewRegistry()
	key := make(ed25519.PublicKey, ed25519.PublicKeySize)
	key[0] = 1

	cases := []struct {
		name string
		key  ed25519.PublicKey
		err  error
	}{
		{"", key, ErrInvalidIdentityName},
		{string([]byte{0xff, 0xfe}), key, ErrInvalidIdentityName},
		{"alice", make(ed25519.PublicKey, 31), ErrInvalidPublicKey},
		{"alice", nil, ErrInvalidPublicKey},
	}
	for _, testCase := range cases {
		if err := registry.Register(testCase.name, testCase.key); !errors.Is(err, testCase.err) {
			t.Fatalf("Register(%q, %d bytes) = %v, want %v", testCase.name, len(testCase.key), err, testCase.err)
		}
	}
}

func TestRegistryUniqueNameAndKeyBindings(t *testing.T) {
	registry := NewRegistry()
	seedA := sha256.Sum256([]byte("a"))
	seedB := sha256.Sum256([]byte("b"))
	keyA := ed25519.NewKeyFromSeed(seedA[:]).Public().(ed25519.PublicKey)
	keyB := ed25519.NewKeyFromSeed(seedB[:]).Public().(ed25519.PublicKey)

	if err := registry.Register("alice", keyA); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register("alice", keyA); !errors.Is(err, ErrIdentityConflict) {
		t.Fatalf("duplicate name error = %v", err)
	}
	if err := registry.Register("alice", keyB); !errors.Is(err, ErrIdentityConflict) {
		t.Fatalf("duplicate name/new key error = %v", err)
	}
	if err := registry.Register("bob", keyA); !errors.Is(err, ErrIdentityConflict) {
		t.Fatalf("duplicate key error = %v", err)
	}
	if err := registry.Register("bob", keyB); err != nil {
		t.Fatal(err)
	}

	identity, err := registry.Lookup("alice")
	if err != nil {
		t.Fatal(err)
	}
	if identity.Name != "alice" || string(identity.PublicKey) != string(keyA) {
		t.Fatalf("unexpected lookup result: %+v", identity)
	}
	identity.PublicKey[0] ^= 0xff
	again, err := registry.Lookup("alice")
	if err != nil {
		t.Fatal(err)
	}
	if string(again.PublicKey) != string(keyA) {
		t.Fatal("lookup returned an alias into registry storage")
	}
	if name, ok := registry.NameFor(keyB); !ok || name != "bob" {
		t.Fatalf("NameFor = %q, %v", name, ok)
	}
	if _, err := registry.Lookup("carol"); !errors.Is(err, ErrIdentityNotFound) {
		t.Fatalf("unknown lookup error = %v", err)
	}
	if registry.Len() != 2 {
		t.Fatalf("Len = %d", registry.Len())
	}
}

func TestNewPartyRequiresMatchingRegisteredPrivateKey(t *testing.T) {
	registry := NewRegistry()
	seedA := sha256.Sum256([]byte("registered"))
	seedB := sha256.Sum256([]byte("different"))
	registered := ed25519.NewKeyFromSeed(seedA[:])
	wrong := ed25519.NewKeyFromSeed(seedB[:])
	if err := registry.Register("alice", registered.Public().(ed25519.PublicKey)); err != nil {
		t.Fatal(err)
	}
	if _, err := NewParty("alice", wrong, registry); !errors.Is(err, ErrInvalidPrivateKey) {
		t.Fatalf("NewParty with wrong key error = %v", err)
	}
	if _, err := NewParty("alice", make(ed25519.PrivateKey, 12), registry); !errors.Is(err, ErrInvalidPrivateKey) {
		t.Fatalf("NewParty with short key error = %v", err)
	}
	if _, err := NewParty("missing", registered, registry); !errors.Is(err, ErrIdentityNotFound) {
		t.Fatalf("NewParty missing identity error = %v", err)
	}
}

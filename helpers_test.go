package kt

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
)

const testDepth = 6

type testKeys struct {
	dirPub  ed25519.PublicKey
	dirPriv ed25519.PrivateKey
	issPub  ed25519.PublicKey
	issPriv ed25519.PrivateKey
}

func mustKeys(t *testing.T) testKeys {
	t.Helper()
	dirPub, dirPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	issPub, issPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return testKeys{dirPub, dirPriv, issPub, issPriv}
}

func mustPrefix(t *testing.T, bits string) BitPrefix {
	t.Helper()
	p, err := PrefixFromString(bits)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func leaf(pk string, revoked bool, counter uint64) LeafValue {
	return LeafValue{PublicKey: []byte(pk), Revoked: revoked, RevocCounter: counter}
}

// referenceTree builds a 6-bit fully expanded tree with a small fixed entry set.
func referenceTree(t *testing.T, epoch uint32, entries ...Entry) *Tree {
	t.Helper()
	tr, err := BuildTree(epoch, testDepth, entries)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

// signRoot builds and signs an epoch root linking to prev (zero for epoch 1).
func signRoot(t *testing.T, k testKeys, epoch uint32, treeRoot Hash, counter uint64, migHash Hash, prev Hash) *SignedRoot {
	t.Helper()
	sr, err := SignSignedRoot(EpochRoot{
		Epoch:         epoch,
		TreeRoot:      treeRoot,
		RevocCounter:  counter,
		MigrationHash: migHash,
		PrevRoot:      prev,
	}, k.dirPriv)
	if err != nil {
		t.Fatal(err)
	}
	return sr
}

func signAuth(t *testing.T, k testKeys, epoch uint32, prefix BitPrefix, op OpKind, newHash Hash, nonce []byte, serial uint64) *SignedAuth {
	t.Helper()
	auth, err := SignAuth(AuthRecord{
		Epoch:   epoch,
		Prefix:  prefix,
		Op:      op,
		NewHash: newHash,
		Nonce:   nonce,
		Serial:  serial,
	}, k.issPriv)
	if err != nil {
		t.Fatal(err)
	}
	return auth
}

func mustSubtree(t *testing.T, tr *Tree, prefix BitPrefix) Hash {
	t.Helper()
	h, _, err := tr.SubtreeHash(prefix)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func expectErrIs(t *testing.T, err, want error) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %v, got nil", want)
	}
	if err != want {
		t.Fatalf("expected error %v, got %v", want, err)
	}
}

func nonce(seed ...byte) []byte {
	out := make([]byte, 16)
	copy(out, seed)
	return out
}

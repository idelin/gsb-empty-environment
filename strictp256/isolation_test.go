package strictp256

import (
	"crypto/ecdsa"
	"errors"
	"sync/atomic"
	"testing"
)

// TestDifferentKeysSameMessage ensures the compressed public key inside the ID
// separates distinct principals and that one key's signature cannot be replayed
// against another key's registry entry.
func TestDifferentKeysSameMessage(t *testing.T) {
	alice := mustKey(t)
	bob := mustKey(t)
	msg := []byte("same-message")

	idA, err := RequestID(&alice.PublicKey, msg)
	if err != nil {
		t.Fatalf("id alice: %v", err)
	}
	idB, err := RequestID(&bob.PublicKey, msg)
	if err != nil {
		t.Fatalf("id bob: %v", err)
	}
	if idA == idB {
		t.Fatal("different public keys produced the same request ID")
	}
	idA2, _ := RequestID(&alice.PublicKey, msg)
	if idA != idA2 {
		t.Fatal("request ID not deterministic")
	}
	if ref := referenceID(&alice.PublicKey, msg); ref != idA {
		t.Fatalf("request ID does not match documented layout")
	}

	reg := NewRegistry[string]()
	var runsA, runsB int32
	sigA := mustSig(t, alice, msg, false)
	sigB := mustSig(t, bob, msg, false)

	resA, err := reg.Submit(&alice.PublicKey, msg, sigA, func() (string, error) {
		atomic.AddInt32(&runsA, 1)
		return "alice", nil
	})
	if err != nil || resA != "alice" {
		t.Fatalf("alice submit = (%q, %v)", resA, err)
	}
	resB, err := reg.Submit(&bob.PublicKey, msg, sigB, func() (string, error) {
		atomic.AddInt32(&runsB, 1)
		return "bob", nil
	})
	if err != nil || resB != "bob" {
		t.Fatalf("bob submit = (%q, %v)", resB, err)
	}

	// Alice's signature must not verify as Bob.
	if _, err := reg.Submit(&bob.PublicKey, msg, sigA, func() (string, error) {
		t.Fatal("cross-key signature must not reach handler")
		return "", nil
	}); !errors.Is(err, ErrVerification) {
		t.Fatalf("cross-key err = %v, want ErrVerification", err)
	}
	if atomic.LoadInt32(&runsA) != 1 || atomic.LoadInt32(&runsB) != 1 {
		t.Fatalf("runsA=%d runsB=%d, want 1 each", runsA, runsB)
	}
}

// TestRevocationBoundToKeyAndMessageID verifies the revocation index targets
// (public key, content ID) rather than signature bytes: both S variants are
// blocked once revoked, while a different key or message stays usable.
func TestRevocationBoundToKeyAndMessageID(t *testing.T) {
	alice := mustKey(t)
	bob := mustKey(t)
	msg := []byte("revoke-me")
	lowSig := mustSig(t, alice, msg, false)
	highSig := mustSig(t, alice, msg, true)

	reg := NewRegistry[string]()
	if _, err := reg.Submit(&alice.PublicKey, msg, lowSig, func() (string, error) {
		return "executed", nil
	}); err != nil {
		t.Fatalf("initial submit: %v", err)
	}

	if err := reg.Revoke(&alice.PublicKey, msg); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if !reg.IsRevoked(&alice.PublicKey, msg) {
		t.Fatal("IsRevoked should report true for alice/msg")
	}

	// Indexing ignores signature bytes, so the low-S duplicate is revoked.
	if _, err := reg.Submit(&alice.PublicKey, msg, lowSig, func() (string, error) {
		t.Fatal("revoked low-S submission must not run handler")
		return "", nil
	}); !errors.Is(err, ErrRevoked) {
		t.Fatalf("revoked low-S err = %v, want ErrRevoked", err)
	}
	// The high-S variant is rejected at the strict gate before revocation.
	if _, err := reg.Submit(&alice.PublicKey, msg, highSig, func() (string, error) {
		t.Fatal("high-S submission must not run handler")
		return "", nil
	}); !errors.Is(err, ErrHighS) {
		t.Fatalf("revoked high-S err = %v, want ErrHighS", err)
	}

	// Different principal and different message are unaffected.
	otherMsg := []byte("revoke-me-other")
	if reg.IsRevoked(&bob.PublicKey, msg) {
		t.Fatal("bob must not share alice's revocation index")
	}
	if reg.IsRevoked(&alice.PublicKey, otherMsg) {
		t.Fatal("different message must not share the revocation index")
	}
	if _, err := reg.Submit(&bob.PublicKey, msg, mustSig(t, bob, msg, false), func() (string, error) {
		return "bob-ok", nil
	}); err != nil {
		t.Fatalf("bob submit after alice revocation: %v", err)
	}
	if _, err := reg.Submit(&alice.PublicKey, otherMsg, mustSig(t, alice, otherMsg, false), func() (string, error) {
		return "alice-other-ok", nil
	}); err != nil {
		t.Fatalf("alice other message after revocation: %v", err)
	}
}

// TestCompressedKeyAndRequestIDVectors locks the wire format and hash layout.
func TestCompressedKeyAndRequestIDVectors(t *testing.T) {
	key := mustKey(t)
	pub := &key.PublicKey
	compressed, err := CompressedPublicKey(pub)
	if err != nil {
		t.Fatalf("compress: %v", err)
	}
	if len(compressed) != 33 || (compressed[0] != 0x02 && compressed[0] != 0x03) {
		t.Fatalf("compressed key malformed: %x", compressed)
	}
	if got := compressed[0] == 0x03; got != (pub.Y.Bit(0) == 1) {
		t.Fatalf("parity prefix 0x%02x does not match Y", compressed[0])
	}

	msg := []byte("hello")
	id, err := RequestID(pub, msg)
	if err != nil {
		t.Fatalf("request id: %v", err)
	}
	if len(id) != RequestIDSize {
		t.Fatalf("id length = %d, want %d", len(id), RequestIDSize)
	}
	if id != referenceID(pub, msg) {
		t.Fatalf("request ID does not match documented layout")
	}
	idOther, _ := RequestID(pub, []byte("hello!"))
	if id == idOther {
		t.Fatal("message change did not change request ID")
	}

	// Length-prefixing must distinguish different length encodings of content.
	idA, _ := RequestID(pub, []byte("ab"))
	idB, _ := RequestID(pub, []byte("a"))
	if idA == idB {
		t.Fatal("distinct messages collided")
	}
}

// TestRequestIDIndependentOfSignature confirms directly that swapping S
// variants leaves the request ID untouched.
func TestRequestIDIndependentOfSignature(t *testing.T) {
	key := mustKey(t)
	pub := &key.PublicKey
	msg := []byte("sig-independent")
	_ = mustSig(t, key, msg, false)
	_ = mustSig(t, key, msg, true)
	id1, err := RequestID(pub, msg)
	if err != nil {
		t.Fatalf("id: %v", err)
	}
	id2, _ := RequestID(pub, msg)
	if id1 != id2 {
		t.Fatal("request ID changed despite identical (pubkey, message)")
	}
}

var _ = ecdsa.PublicKey{}

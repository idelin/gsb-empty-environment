package netsec

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"testing"
)

// testKey returns a deterministic-ish random 32-byte AES key.
func testKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return key
}

// genKey derives a stable 32-byte key for a generation so that a rebuilt
// encryptor after a simulated restart can open previously sealed data.
func genKey(gen KeyGen) []byte {
	sum := sha256.Sum256([]byte(fmt.Sprintf("netsec-test-key-gen:%d", gen)))
	return sum[:]
}

// mustSeal builds an AES-GCM encryptor provisioned with gen 0 and a fresh ring.
func mustSeal(t *testing.T) (*AESGCMEncryptor, *KeyRing) {
	t.Helper()
	enc, err := NewAESGCMEncryptor(testKey(t))
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}
	return enc, NewKeyRing()
}

// addKey provisions key material for gen on the encryptor.
func addKey(t *testing.T, enc *AESGCMEncryptor, gen KeyGen) {
	t.Helper()
	if err := enc.Rotate(gen, testKey(t)); err != nil {
		t.Fatalf("provision gen %d: %v", gen, err)
	}
}

// rotate provisions the next generation on the encryptor and rotates the ring.
func rotate(t *testing.T, ctx context.Context, enc Encryptor, ring *KeyRing) KeyGen {
	t.Helper()
	next := ring.Current() + 1
	if aes, ok := enc.(*AESGCMEncryptor); ok {
		addKey(t, aes, next)
	} else if p, ok := enc.(*PausingEncryptor); ok {
		if aes, ok := p.inner.(*AESGCMEncryptor); ok {
			addKey(t, aes, next)
		}
	}
	if err := ring.Rotate(next); err != nil {
		t.Fatalf("rotate to %d: %v", next, err)
	}
	return next
}

// rotateDet provisions a deterministically derived key for the next
// generation so a rebuilt encryptor after restart holds the same material.
func rotateDet(t *testing.T, enc *AESGCMEncryptor, ring *KeyRing) KeyGen {
	t.Helper()
	next := ring.Current() + 1
	if err := enc.Rotate(next, genKey(next)); err != nil {
		t.Fatalf("provision gen %d: %v", next, err)
	}
	if err := ring.Rotate(next); err != nil {
		t.Fatalf("rotate to %d: %v", next, err)
	}
	return next
}

// newTestVolume assembles a volume over the given store with real AES-GCM.
func newTestVolume(t *testing.T, ctx context.Context, store Store, hooks Hooks) (*Volume, *AESGCMEncryptor, *KeyRing) {
	t.Helper()
	enc, ring := mustSeal(t)
	vol, err := NewVolume(ctx, store, ring, enc, hooks)
	if err != nil {
		t.Fatalf("new volume: %v", err)
	}
	return vol, enc, ring
}

// newDeterministicVolume builds a volume whose gen-0 key is derived
// deterministically, so a rebuilt host over the same store can reopen it.
func newDeterministicVolume(t *testing.T, ctx context.Context, store Store, hooks Hooks) (*Volume, *AESGCMEncryptor, *KeyRing) {
	t.Helper()
	enc, err := NewAESGCMEncryptor(genKey(0))
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}
	ring := NewKeyRing()
	vol, err := NewVolume(ctx, store, ring, enc, hooks)
	if err != nil {
		t.Fatalf("new volume: %v", err)
	}
	return vol, enc, ring
}

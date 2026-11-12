package netsec

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func identityFor(label string) (ed25519.PublicKey, ed25519.PrivateKey) {
	seed := sha256.Sum256([]byte("identity:" + label))
	privateKey := ed25519.NewKeyFromSeed(seed[:])
	return privateKey.Public().(ed25519.PublicKey), privateKey
}

func publicKeyFor(label string) []byte {
	sum := sha256.Sum256([]byte("prekey:" + label))
	return append([]byte(nil), sum[:]...)
}

func signedPreKeyFor(t *testing.T, identityPrivateKey ed25519.PrivateKey, id uint32, label string) SignedPreKey {
	t.Helper()
	publicKey := publicKeyFor(label)
	signature, err := SignSignedPreKey(identityPrivateKey, publicKey)
	if err != nil {
		t.Fatalf("sign signed prekey: %v", err)
	}
	return SignedPreKey{ID: id, PublicKey: publicKey, Signature: signature}
}

func oneTimePreKeysFor(start, count uint32) []OneTimePreKey {
	keys := make([]OneTimePreKey, count)
	for i := range keys {
		keys[i] = OneTimePreKey{
			ID:        start + uint32(i),
			PublicKey: publicKeyFor(fmt.Sprintf("one-time:%d", start+uint32(i))),
		}
	}
	return keys
}

func TestSignedPreKeySignatureCoversTypeByte(t *testing.T) {
	identityKey, identityPrivateKey := identityFor("alice")
	signedPreKey := signedPreKeyFor(t, identityPrivateKey, 1, "alice-signed")

	if got := EncodePublicKey(signedPreKey.PublicKey); len(got) != len(signedPreKey.PublicKey)+1 || got[0] != Curve25519KeyType {
		t.Fatalf("encoded public key = %x, want type byte %x followed by public key", got, Curve25519KeyType)
	}

	rawSignature := ed25519.Sign(identityPrivateKey, signedPreKey.PublicKey)
	store := NewStore()
	err := store.Register("alice", identityKey, SignedPreKey{
		ID:        signedPreKey.ID,
		PublicKey: signedPreKey.PublicKey,
		Signature: rawSignature,
	}, nil)
	if !errors.Is(err, ErrInvalidSignedPreKeySig) {
		t.Fatalf("register with signature over unencoded key: error = %v, want %v", err, ErrInvalidSignedPreKeySig)
	}

	if err := store.Register("alice", identityKey, signedPreKey, oneTimePreKeysFor(10, 1)); err != nil {
		t.Fatalf("register valid signed prekey: %v", err)
	}
	bundle, err := store.FetchPreKeyBundle("alice")
	if err != nil {
		t.Fatalf("fetch bundle: %v", err)
	}
	if !VerifySignedPreKey(bundle.IdentityKey, bundle.SignedPreKey) {
		t.Fatal("bundle signed prekey signature does not verify over encoded public key")
	}
	if !bytes.Equal(bundle.IdentityKey, identityKey) {
		t.Fatal("bundle identity key does not match registration")
	}
}

func TestConcurrentFetchesAllocateEachOneTimePreKeyExactlyOnce(t *testing.T) {
	identityKey, identityPrivateKey := identityFor("alice")
	signedPreKey := signedPreKeyFor(t, identityPrivateKey, 1, "alice-signed")
	const available = 17
	const requests = 240
	oneTimeKeys := oneTimePreKeysFor(100, available)

	store := NewStore()
	if err := store.Register("alice", identityKey, signedPreKey, oneTimeKeys); err != nil {
		t.Fatalf("register: %v", err)
	}

	type result struct {
		bundle *PreKeyBundle
		err    error
	}
	results := make(chan result, requests)
	var wg sync.WaitGroup
	wg.Add(requests)
	for i := 0; i < requests; i++ {
		go func() {
			defer wg.Done()
			bundle, err := store.FetchPreKeyBundle("alice")
			results <- result{bundle: bundle, err: err}
		}()
	}
	wg.Wait()
	close(results)

	expected := make(map[uint32][]byte, available)
	for _, key := range oneTimeKeys {
		expected[key.ID] = key.PublicKey
	}
	seen := make(map[uint32][]byte, available)
	withOneTime := 0
	for result := range results {
		if result.err != nil {
			t.Fatalf("fetch returned error: %v", result.err)
		}
		if result.bundle == nil || !bytes.Equal(result.bundle.IdentityKey, identityKey) {
			t.Fatalf("invalid bundle: %+v", result.bundle)
		}
		if !VerifySignedPreKey(result.bundle.IdentityKey, result.bundle.SignedPreKey) {
			t.Fatal("bundle contains an invalid signed prekey")
		}
		if result.bundle.OneTimePreKey == nil {
			continue
		}
		withOneTime++
		got := result.bundle.OneTimePreKey
		if previous, ok := seen[got.ID]; ok {
			t.Fatalf("one-time prekey ID %d issued twice (first key %x, second key %x)", got.ID, previous, got.PublicKey)
		}
		seen[got.ID] = got.PublicKey
		if want, ok := expected[got.ID]; !ok || !bytes.Equal(got.PublicKey, want) {
			t.Fatalf("unexpected one-time prekey ID %d: %x", got.ID, got.PublicKey)
		}
	}
	if withOneTime != available {
		t.Fatalf("bundles with one-time prekey = %d, want %d", withOneTime, available)
	}
	if len(seen) != available {
		t.Fatalf("unique one-time prekeys issued = %d, want %d", len(seen), available)
	}
	if remaining, err := store.PendingOneTimePreKeyCount("alice"); err != nil || remaining != 0 {
		t.Fatalf("remaining one-time prekeys = %d, err = %v; want 0, nil", remaining, err)
	}
}

func TestExhaustedPoolReturnsBundleWithoutOneTimePreKey(t *testing.T) {
	identityKey, identityPrivateKey := identityFor("bob")
	signedPreKey := signedPreKeyFor(t, identityPrivateKey, 2, "bob-signed")

	store := NewStore()
	if err := store.Register("bob", identityKey, signedPreKey, oneTimePreKeysFor(20, 2)); err != nil {
		t.Fatalf("register: %v", err)
	}

	for i := 0; i < 2; i++ {
		bundle, err := store.FetchPreKeyBundle("bob")
		if err != nil {
			t.Fatalf("fetch %d: %v", i, err)
		}
		if bundle.OneTimePreKey == nil {
			t.Fatalf("fetch %d unexpectedly omitted one-time prekey", i)
		}
	}

	for i := 0; i < 4; i++ {
		bundle, err := store.FetchPreKeyBundle("bob")
		if err != nil {
			t.Fatalf("exhausted fetch %d: %v", i, err)
		}
		if bundle.OneTimePreKey != nil {
			t.Fatalf("exhausted fetch %d returned one-time prekey ID %d", i, bundle.OneTimePreKey.ID)
		}
		if bundle.HasOneTimePreKey() {
			t.Fatalf("exhausted fetch %d reports a one-time prekey", i)
		}
		if !bytes.Equal(bundle.IdentityKey, identityKey) {
			t.Fatalf("exhausted fetch %d has wrong identity key", i)
		}
		if !VerifySignedPreKey(bundle.IdentityKey, bundle.SignedPreKey) {
			t.Fatalf("exhausted fetch %d has invalid signed prekey", i)
		}
	}
}

func TestIdentityKeyCannotBeReplaced(t *testing.T) {
	identityKey, identityPrivateKey := identityFor("alice")
	otherIdentityKey, otherIdentityPrivateKey := identityFor("mallory")
	signedPreKey := signedPreKeyFor(t, identityPrivateKey, 3, "alice-signed")
	otherSignedPreKey := signedPreKeyFor(t, otherIdentityPrivateKey, 4, "mallory-signed")

	store := NewStore()
	if err := store.Register("alice", identityKey, signedPreKey, oneTimePreKeysFor(30, 2)); err != nil {
		t.Fatalf("initial register: %v", err)
	}

	err := store.Register("alice", otherIdentityKey, otherSignedPreKey, oneTimePreKeysFor(40, 1))
	if !errors.Is(err, ErrIdentityKeyMismatch) {
		t.Fatalf("identity replacement error = %v, want %v", err, ErrIdentityKeyMismatch)
	}

	bundle, err := store.FetchPreKeyBundle("alice")
	if err != nil {
		t.Fatalf("fetch after rejected replacement: %v", err)
	}
	if !bytes.Equal(bundle.IdentityKey, identityKey) {
		t.Fatalf("identity key was replaced: got %x, want %x", bundle.IdentityKey, identityKey)
	}
	if bundle.SignedPreKey.ID != signedPreKey.ID {
		t.Fatalf("signed prekey was replaced after rejected registration: got ID %d, want %d", bundle.SignedPreKey.ID, signedPreKey.ID)
	}

	before, err := store.PendingOneTimePreKeyCount("alice")
	if err != nil {
		t.Fatalf("pending count before claimed mismatch: %v", err)
	}
	_, err = store.FetchPreKeyBundle("alice", otherIdentityKey)
	if !errors.Is(err, ErrIdentityKeyMismatch) {
		t.Fatalf("fetch with wrong claimed identity: error = %v, want %v", err, ErrIdentityKeyMismatch)
	}
	after, err := store.PendingOneTimePreKeyCount("alice")
	if err != nil {
		t.Fatalf("pending count after claimed mismatch: %v", err)
	}
	if before != after {
		t.Fatalf("identity mismatch consumed a one-time prekey: before %d, after %d", before, after)
	}

	if err := store.RotateSignedPreKey("alice", otherIdentityKey, otherSignedPreKey); !errors.Is(err, ErrIdentityKeyMismatch) {
		t.Fatalf("rotation with replaced identity: error = %v, want %v", err, ErrIdentityKeyMismatch)
	}
}

func TestDuplicateOneTimePreKeysAreRejected(t *testing.T) {
	identityKey, identityPrivateKey := identityFor("alice")
	signedPreKey := signedPreKeyFor(t, identityPrivateKey, 5, "alice-signed")
	store := NewStore()

	if err := store.Register("alice", identityKey, signedPreKey, oneTimePreKeysFor(50, 1)); err != nil {
		t.Fatalf("initial register: %v", err)
	}
	err := store.AddOneTimePreKeys("alice", oneTimePreKeysFor(50, 1))
	if !errors.Is(err, ErrDuplicateOneTimePreKey) {
		t.Fatalf("duplicate ID error = %v, want %v", err, ErrDuplicateOneTimePreKey)
	}

	duplicate := []OneTimePreKey{
		{ID: 60, PublicKey: publicKeyFor("duplicate")},
		{ID: 60, PublicKey: publicKeyFor("duplicate")},
	}
	err = store.AddOneTimePreKeys("alice", duplicate)
	if !errors.Is(err, ErrDuplicateOneTimePreKey) {
		t.Fatalf("duplicate batch error = %v, want %v", err, ErrDuplicateOneTimePreKey)
	}
	if remaining, err := store.PendingOneTimePreKeyCount("alice"); err != nil || remaining != 1 {
		t.Fatalf("failed add mutated store: remaining %d, err %v; want 1, nil", remaining, err)
	}
}

func TestCallerMutationDoesNotChangeStoredKeyMaterial(t *testing.T) {
	identityKey, identityPrivateKey := identityFor("alice")
	signedPreKey := signedPreKeyFor(t, identityPrivateKey, 6, "alice-signed")
	oneTimeKeys := oneTimePreKeysFor(70, 1)

	store := NewStore()
	if err := store.Register("alice", identityKey, signedPreKey, oneTimeKeys); err != nil {
		t.Fatalf("register: %v", err)
	}

	identityKey[0] ^= 0xff
	signedPreKey.PublicKey[0] ^= 0xff
	signedPreKey.Signature[0] ^= 0xff
	oneTimeKeys[0].PublicKey[0] ^= 0xff

	bundle, err := store.FetchPreKeyBundle("alice")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if !VerifySignedPreKey(bundle.IdentityKey, bundle.SignedPreKey) {
		t.Fatal("mutating caller-owned slices changed stored signed prekey")
	}
	if bundle.OneTimePreKey == nil || bytes.Equal(bundle.OneTimePreKey.PublicKey, oneTimeKeys[0].PublicKey) {
		t.Fatal("mutating caller-owned slice changed stored one-time prekey")
	}

	bundle.IdentityKey[0] ^= 0xff
	bundle.SignedPreKey.PublicKey[0] ^= 0xff
	bundle.OneTimePreKey.PublicKey[0] ^= 0xff

	next, err := store.FetchPreKeyBundle("alice")
	if err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	if next.OneTimePreKey != nil {
		t.Fatal("returned one-time prekey was reissued")
	}
	if !VerifySignedPreKey(next.IdentityKey, next.SignedPreKey) {
		t.Fatal("mutating a returned bundle changed stored state")
	}
}

func TestUnknownUserIsRejected(t *testing.T) {
	store := NewStore()
	if _, err := store.FetchPreKeyBundle("nobody"); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("fetch unknown user: error = %v, want %v", err, ErrUserNotFound)
	}
	if _, err := store.PendingOneTimePreKeyCount("nobody"); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("count unknown user: error = %v, want %v", err, ErrUserNotFound)
	}
}

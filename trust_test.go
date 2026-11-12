package taskpkg

import (
	"errors"
	"testing"
	"time"
)

// A signature that was valid before revocation must not re-poison the cache
// when the answer arrives after the key was revoked.
func TestOldKeyLateAnswerBecomesDiagnostic(t *testing.T) {
	const zone = "example."
	oldKey := genTestKey(t)
	store := NewTrustStore()
	if _, err := store.Bootstrap(zone, 1, keyMap(oldKey)); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	q := Query{ID: 7, QName: "www.example.", Anchor: TrustAnchor{Zone: zone, Version: 1}}
	resolver := NewResolver(store)
	ans := signAnswer(t, oldKey, zone, q.ID, q.QName, 1, []byte("192.0.2.10"))
	va := mustVerify(t, resolver, q, ans)

	cache := NewCache()
	pub := NewPausablePublisher(store, cache)
	pub.Pause()
	key := CacheKey{Zone: zone, QName: q.QName}
	if err := pub.Submit(key, va); err != nil {
		t.Fatalf("submit while paused: %v", err)
	}

	if got := cache.AuthCount() + cache.DiagnosticCount(); got != 0 {
		t.Fatalf("cache mutated while paused: %d entries", got)
	}

	newKey := genTestKey(t)
	if _, err := store.Update(zone, 2, keyMap(newKey)); err != nil {
		t.Fatalf("update: %v", err)
	}
	if store.IsKeyActive(zone, oldKey.fp) {
		t.Fatal("old key must be inactive after rotation")
	}

	pub.Resume()
	if !pub.WaitIdle(2 * time.Second) {
		t.Fatal("publisher did not drain queued answers")
	}
	pub.Close()

	if _, ok := cache.Lookup(key); ok {
		t.Fatal("revoked-key answer must not authorize new queries")
	}
	diag, ok := cache.Diagnostic(key)
	if !ok {
		t.Fatal("revoked-key answer must be retained as diagnostic")
	}
	if diag.State != Diagnostic {
		t.Fatalf("diagnostic entry state = %v, want Diagnostic", diag.State)
	}
	if diag.Path.Active {
		t.Fatal("trust path must mark the revoked signer inactive")
	}
	if diag.Path.CurrentVersion != 2 || diag.Path.AnchoredVersion != 1 {
		t.Fatalf("trust path versions = anchored %d current %d, want 1/2",
			diag.Path.AnchoredVersion, diag.Path.CurrentVersion)
	}
	wantHops := []Hop{{Version: 1, Retained: true}, {Version: 2, Retained: false}}
	if len(diag.Path.Hops) != len(wantHops) {
		t.Fatalf("hops = %v, want %v", diag.Path.Hops, wantHops)
	}
	for i, h := range diag.Path.Hops {
		if h != wantHops[i] {
			t.Fatalf("hop %d = %+v, want %+v", i, h, wantHops[i])
		}
	}
}

// Trust is decided by the retained fingerprint allow-list, not by version
// ordering: a key retained across generations stays authoritative even
// though its answer was anchored to an older version.
func TestRetainedFingerprintSurvivesRotation(t *testing.T) {
	const zone = "example.org."
	kept := genTestKey(t)
	retired := genTestKey(t)
	store := NewTrustStore()
	if _, err := store.Bootstrap(zone, 10, keyMap(kept, retired)); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	q := Query{ID: 3, QName: "mail.example.org.", Anchor: TrustAnchor{Zone: zone, Version: 10}}
	resolver := NewResolver(store)
	keptAnswer := signAnswer(t, kept, zone, q.ID, q.QName, 10, []byte("192.0.2.20"))
	retiredAnswer := signAnswer(t, retired, zone, q.ID, q.QName, 10, []byte("192.0.2.30"))
	keptVA := mustVerify(t, resolver, q, keptAnswer)
	retiredVA := mustVerify(t, resolver, q, retiredAnswer)

	if _, err := store.Update(zone, 20, keyMap(kept)); err != nil {
		t.Fatalf("update: %v", err)
	}

	cache := NewCache()
	pub := NewPausablePublisher(store, cache)
	keptKey := CacheKey{Zone: zone, QName: q.QName}
	retiredKey := CacheKey{Zone: zone, QName: "old.example.org."}
	if err := pub.PublishNow(keptKey, keptVA); err != nil {
		t.Fatalf("publish retained: %v", err)
	}
	if err := pub.PublishNow(retiredKey, retiredVA); err != nil {
		t.Fatalf("publish retired: %v", err)
	}
	pub.Close()

	auth, ok := cache.Lookup(keptKey)
	if !ok || auth.State != Authoritative {
		t.Fatal("retained fingerprint must stay authoritative across rotation")
	}
	if !auth.Path.Active || auth.Path.CurrentVersion != 20 {
		t.Fatalf("retained entry path = %+v", auth.Path)
	}
	for _, hop := range auth.Path.Hops {
		if !hop.Retained {
			t.Fatalf("retained key shows non-retained hop: %+v", hop)
		}
	}
	if _, ok := cache.Lookup(retiredKey); ok {
		t.Fatal("dropped fingerprint must not be authoritative regardless of equal versions")
	}
	diag, ok := cache.Diagnostic(retiredKey)
	if !ok || diag.State != Diagnostic {
		t.Fatal("dropped fingerprint answer must remain diagnostic")
	}
	if diag.Path.Active {
		t.Fatal("retired key path must be inactive")
	}
}

// A different key signing at the exact pinned version is rejected, proving
// trust cannot be gained by reusing a valid version number.
func TestSameVersionDifferentKeyRejected(t *testing.T) {
	const zone = "example.net."
	legit := genTestKey(t)
	rogue := genTestKey(t)
	store := NewTrustStore()
	if _, err := store.Bootstrap(zone, 5, keyMap(legit)); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	q := Query{ID: 9, QName: "api.example.net.", Anchor: TrustAnchor{Zone: zone, Version: 5}}
	resolver := NewResolver(store)

	rogueAnswer := signAnswer(t, rogue, zone, q.ID, q.QName, 5, []byte("192.0.2.40"))
	if _, err := resolver.Verify(q, rogueAnswer); !errors.Is(err, ErrFingerprintNotRetained) {
		t.Fatalf("rogue same-version key: got %v, want ErrFingerprintNotRetained", err)
	}

	if _, err := store.Update(zone, 5, keyMap(rogue)); !errors.Is(err, ErrVersionOutOfOrder) {
		t.Fatalf("same-version redeclaration: got %v, want ErrVersionOutOfOrder", err)
	}

	good := signAnswer(t, legit, zone, q.ID, q.QName, 5, []byte("192.0.2.50"))
	good.Signature[0] ^= 0xFF
	if _, err := resolver.Verify(q, good); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("tampered signature: got %v, want ErrBadSignature", err)
	}
}

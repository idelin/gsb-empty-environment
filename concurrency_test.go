package taskpkg

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// Each query is pinned to one authoritative zone; answers from other zones
// cannot be accepted even if they are validly signed there.
func TestZoneAndAnchorPinning(t *testing.T) {
	const zoneA = "a.example."
	const zoneB = "b.example."
	keyA := genTestKey(t)
	keyB := genTestKey(t)
	store := NewTrustStore()
	if _, err := store.Bootstrap(zoneA, 1, keyMap(keyA)); err != nil {
		t.Fatalf("bootstrap A: %v", err)
	}
	if _, err := store.Bootstrap(zoneB, 1, keyMap(keyB)); err != nil {
		t.Fatalf("bootstrap B: %v", err)
	}
	resolver := NewResolver(store)
	q := Query{ID: 1, QName: "host.a.example.", Anchor: TrustAnchor{Zone: zoneA, Version: 1}}

	foreign := signAnswer(t, keyB, zoneB, q.ID, q.QName, 1, []byte("192.0.2.60"))
	if _, err := resolver.Verify(q, foreign); !errors.Is(err, ErrZoneMismatch) {
		t.Fatalf("foreign zone: got %v, want ErrZoneMismatch", err)
	}

	wrongVersion := signAnswer(t, keyA, zoneA, q.ID, q.QName, 2, []byte("192.0.2.61"))
	if _, err := resolver.Verify(q, wrongVersion); !errors.Is(err, ErrAnchorMismatch) {
		t.Fatalf("wrong anchor version: got %v, want ErrAnchorMismatch", err)
	}

	wrongQuestion := signAnswer(t, keyA, zoneA, 2, "other.a.example.", 1, []byte("192.0.2.62"))
	if _, err := resolver.Verify(q, wrongQuestion); !errors.Is(err, ErrQuestionMismatch) {
		t.Fatalf("wrong question: got %v, want ErrQuestionMismatch", err)
	}

	if _, err := store.At(zoneA, 42); !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("unknown version: got %v, want ErrUnknownVersion", err)
	}
	if _, err := store.Current("nope."); !errors.Is(err, ErrUnknownZone) {
		t.Fatalf("unknown zone: got %v, want ErrUnknownZone", err)
	}
}

// Two zones rotate concurrently while answers are paused; each cache entry
// must carry the trust path of its own zone and never cross zones.
func TestDualZoneConcurrency(t *testing.T) {
	const zoneA = "alpha.example."
	const zoneB = "bravo.example."
	keyA := genTestKey(t)
	keyB := genTestKey(t)
	store := NewTrustStore()
	if _, err := store.Bootstrap(zoneA, 1, keyMap(keyA)); err != nil {
		t.Fatalf("bootstrap A: %v", err)
	}
	if _, err := store.Bootstrap(zoneB, 1, keyMap(keyB)); err != nil {
		t.Fatalf("bootstrap B: %v", err)
	}

	cache := NewCache()
	pub := NewPausablePublisher(store, cache)
	pub.Pause()
	resolver := NewResolver(store)

	const perZone = 50
	var wg sync.WaitGroup
	for i := 0; i < perZone; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			q := Query{ID: uint64(i), QName: fmt.Sprintf("h%d.alpha.example.", i), Anchor: TrustAnchor{Zone: zoneA, Version: 1}}
			ans := signAnswer(t, keyA, zoneA, q.ID, q.QName, 1, []byte(fmt.Sprintf("10.0.%d.1", i)))
			if err := pub.Submit(CacheKey{Zone: zoneA, QName: q.QName}, mustVerify(t, resolver, q, ans)); err != nil {
				t.Errorf("submit A: %v", err)
			}
		}(i)
		go func(i int) {
			defer wg.Done()
			q := Query{ID: uint64(i), QName: fmt.Sprintf("h%d.bravo.example.", i), Anchor: TrustAnchor{Zone: zoneB, Version: 1}}
			ans := signAnswer(t, keyB, zoneB, q.ID, q.QName, 1, []byte(fmt.Sprintf("10.1.%d.1", i)))
			if err := pub.Submit(CacheKey{Zone: zoneB, QName: q.QName}, mustVerify(t, resolver, q, ans)); err != nil {
				t.Errorf("submit B: %v", err)
			}
		}(i)
	}
	wg.Wait()

	// While paused, one item may already be held by the blocked worker
	// gate; in-flight still accounts for the whole batch and nothing may
	// have reached the cache.
	if got := pub.inflight.Load(); got != int64(2*perZone) {
		t.Fatalf("in-flight = %d, want %d", got, 2*perZone)
	}
	if cache.PublishCount() != 0 {
		t.Fatalf("cache published %d entries while paused", cache.PublishCount())
	}

	// Rotate while the batch is queued: A keeps its key, B rotates and
	// revokes the old one.
	newKeyB := genTestKey(t)
	if _, err := store.Update(zoneA, 2, keyMap(keyA)); err != nil {
		t.Fatalf("update A: %v", err)
	}
	if _, err := store.Update(zoneB, 2, keyMap(newKeyB)); err != nil {
		t.Fatalf("update B: %v", err)
	}

	pub.Resume()
	if !pub.WaitIdle(5 * time.Second) {
		t.Fatal("publisher did not drain concurrent batch")
	}
	pub.Close()

	if cache.PublishCount() != 2*perZone {
		t.Fatalf("publish count = %d, want %d", cache.PublishCount(), 2*perZone)
	}
	if cache.AuthCount() != perZone {
		t.Fatalf("authoritative count = %d, want %d (only zone A kept its key)", cache.AuthCount(), perZone)
	}
	if cache.DiagnosticCount() != perZone {
		t.Fatalf("diagnostic count = %d, want %d (all zone B old-key answers)", cache.DiagnosticCount(), perZone)
	}

	for i := 0; i < perZone; i++ {
		keyAEntry, ok := cache.Lookup(CacheKey{Zone: zoneA, QName: fmt.Sprintf("h%d.alpha.example.", i)})
		if !ok {
			t.Fatalf("zone A entry %d missing from authoritative cache", i)
		}
		if keyAEntry.Path.Zone != zoneA || keyAEntry.Path.Signer != keyA.fp || !keyAEntry.Path.Active {
			t.Fatalf("zone A entry %d has wrong trust path: %+v", i, keyAEntry.Path)
		}
		if keyAEntry.Path.CurrentVersion != 2 || len(keyAEntry.Path.Hops) != 2 {
			t.Fatalf("zone A entry %d hops wrong: %+v", i, keyAEntry.Path.Hops)
		}

		bKey := CacheKey{Zone: zoneB, QName: fmt.Sprintf("h%d.bravo.example.", i)}
		if _, ok := cache.Lookup(bKey); ok {
			t.Fatalf("zone B revoked entry %d must never authorize a new query", i)
		}
		bDiag, ok := cache.Diagnostic(bKey)
		if !ok {
			t.Fatalf("zone B entry %d missing from diagnostic cache", i)
		}
		if bDiag.Path.Zone != zoneB || bDiag.Path.Signer != keyB.fp || bDiag.Path.Active {
			t.Fatalf("zone B entry %d has wrong trust path: %+v", i, bDiag.Path)
		}
		want := []Hop{{Version: 1, Retained: true}, {Version: 2, Retained: false}}
		for j, hop := range bDiag.Path.Hops {
			if hop != want[j] {
				t.Fatalf("zone B entry %d hop %d = %+v, want %+v", i, j, hop, want[j])
			}
		}
	}

	if err := pub.Submit(CacheKey{}, nil); !errors.Is(err, ErrPublisherClosed) {
		t.Fatalf("submit after close: got %v, want ErrPublisherClosed", err)
	}
}

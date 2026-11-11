package netsec

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

// collisionHash maps chosen contents onto a shared short digest, modelling a
// legacy algorithm with injected collisions. Everything outside the collision
// set keeps a unique digest.
type collisionHash struct {
	collide map[string]bool
	tag     byte
}

func (h *collisionHash) Sum(content []byte) Digest {
	if h.collide[string(content)] {
		return Digest{h.tag, 0xFF}
	}
	out := make(Digest, 0, len(content)+1)
	out = append(out, h.tag)
	out = append(out, content...)
	return out
}

func legacyHash(collide ...string) Hash {
	set := make(map[string]bool, len(collide))
	for _, content := range collide {
		set[content] = true
	}
	h := &collisionHash{collide: set, tag: 0x01}
	return h.Sum
}

func strongHash() Hash {
	h := &collisionHash{collide: map[string]bool{}, tag: 0x02}
	return h.Sum
}

// failPoint marks at which call number each wrapped method must fail.
type failPoint struct {
	candidates int64
	lookupNew  int64
	putNew     int64
	rangeOld   int64
}

// FaultStore wraps a Store and injects one error at a chosen call number per
// method, simulating point-wise storage failures during migration.
type FaultStore struct {
	inner Store

	calls failPoint
	fail  failPoint
}

var errInjected = errors.New("netsec: injected storage fault")

func newFaultStore(inner Store) *FaultStore {
	return &FaultStore{inner: inner}
}

func (f *FaultStore) Candidates(oldDigest Digest) ([]*Blob, error) {
	if atomic.AddInt64(&f.calls.candidates, 1) == atomic.LoadInt64(&f.fail.candidates) {
		return nil, errInjected
	}
	return f.inner.Candidates(oldDigest)
}

func (f *FaultStore) LookupNew(newDigest Digest) (*Blob, error) {
	if atomic.AddInt64(&f.calls.lookupNew, 1) == atomic.LoadInt64(&f.fail.lookupNew) {
		return nil, errInjected
	}
	return f.inner.LookupNew(newDigest)
}

func (f *FaultStore) PutNew(blob *Blob) error {
	if atomic.AddInt64(&f.calls.putNew, 1) == atomic.LoadInt64(&f.fail.putNew) {
		return errInjected
	}
	return f.inner.PutNew(blob)
}

func (f *FaultStore) RangeOld(fn func(oldDigest Digest) error) error {
	if atomic.AddInt64(&f.calls.rangeOld, 1) == atomic.LoadInt64(&f.fail.rangeOld) {
		return errInjected
	}
	return f.inner.RangeOld(fn)
}

func seedStore(t *testing.T, contents map[string]int, oldH Hash) (*MemoryStore, map[string]Digest) {
	t.Helper()
	store := NewMemoryStore()
	oldDigests := make(map[string]Digest, len(contents))
	for content, refs := range contents {
		blob := store.AddOld([]byte(content), oldH, refs)
		oldDigests[content] = blob.Old
	}
	return store, oldDigests
}

func sumRefs(refs map[string]int) int {
	total := 0
	for _, n := range refs {
		total += n
	}
	return total
}

func sameRefMap(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}

func TestResolveUnambiguousEstablishesNewIdentity(t *testing.T) {
	oldH, newH := legacyHash(), strongHash()
	store, oldDigests := seedStore(t, map[string]int{"alpha": 3, "beta": 7}, oldH)
	migrator := NewMigrator(store, oldH, newH)

	got, err := migrator.Resolve(oldDigests["alpha"])
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Resolve returned %d digests, want 1", len(got))
	}
	if !got[0].Equal(newH([]byte("alpha"))) {
		t.Fatalf("new identity = %x, want strong hash of content", got[0])
	}

	again, err := migrator.Resolve(oldDigests["alpha"])
	if err != nil || len(again) != 1 || !again[0].Equal(got[0]) {
		t.Fatalf("Resolve not idempotent: %v %v", again, err)
	}
	if store.NewCount() != 1 {
		t.Fatalf("NewCount = %d, want one identity installed once", store.NewCount())
	}

	_, newRefs := store.SnapshotRefs()
	if newRefs[string(got[0])] != 3 {
		t.Fatalf("migrated refs = %d, want 3", newRefs[string(got[0])])
	}
}

func TestResolveCollisionReturnsExplicitAmbiguity(t *testing.T) {
	oldH, newH := legacyHash("alpha", "ALPHA"), strongHash()
	store, oldDigests := seedStore(t, map[string]int{"alpha": 2, "ALPHA": 5, "beta": 1}, oldH)
	migrator := NewMigrator(store, oldH, newH)

	shared := oldDigests["alpha"]
	if !shared.Equal(oldDigests["ALPHA"]) {
		t.Fatal("test setup: collision hash did not collapse the two contents")
	}

	got, err := migrator.Resolve(shared)
	var ambiguous *AmbiguityError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("Resolve error = %v, want *AmbiguityError", err)
	}
	if !ambiguous.OldDigest.Equal(shared) {
		t.Fatalf("ambiguity old digest = %x, want %x", ambiguous.OldDigest, shared)
	}
	want := map[string]bool{
		string(newH([]byte("alpha"))): true,
		string(newH([]byte("ALPHA"))): true,
	}
	if len(got) != 2 || !want[string(got[0])] || !want[string(got[1])] {
		t.Fatalf("ambiguous identities = %x, want both strong hashes", got)
	}

	if store.NewCount() != 2 {
		t.Fatalf("NewCount = %d, colliding objects must keep separate identities", store.NewCount())
	}

	_, newRefs := store.SnapshotRefs()
	if sumRefs(newRefs) != 7 {
		t.Fatalf("migrated refs = %d, want 7 conserved across ambiguity", sumRefs(newRefs))
	}

	if _, err = migrator.Resolve(shared); !errors.As(err, &ambiguous) {
		t.Fatalf("retry error = %v, want persistent ambiguity", err)
	}

	clean, err := migrator.Resolve(oldDigests["beta"])
	if err != nil || len(clean) != 1 {
		t.Fatalf("clean key resolve failed: %v %v", clean, err)
	}
}

func TestDuplicateCandidatesAggregateRefsOnOneIdentity(t *testing.T) {
	oldH, newH := legacyHash(), strongHash()
	store := NewMemoryStore()
	content := []byte("repeat")
	store.AddOld(content, oldH, 4)
	store.AddOld(content, oldH, 6)
	migrator := NewMigrator(store, oldH, newH)

	got, err := migrator.Resolve(oldH(content))
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Resolve returned %d identities for duplicate entries, want 1", len(got))
	}
	_, newRefs := store.SnapshotRefs()
	if newRefs[string(got[0])] != 10 {
		t.Fatalf("aggregated refs = %d, want 10", newRefs[string(got[0])])
	}
}

func TestResolveUnknownOldDigest(t *testing.T) {
	migrator := NewMigrator(NewMemoryStore(), legacyHash(), strongHash())
	if _, err := migrator.Resolve(Digest("missing")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Resolve error = %v, want ErrNotFound", err)
	}
}

func TestPointWiseFaultsRecoverAndConserveRefs(t *testing.T) {
	contents := map[string]int{"alpha": 2, "ALPHA": 3, "beta": 9}

	cases := []struct {
		name string
		fail func(f *FaultStore)
	}{
		{"candidates fault", func(f *FaultStore) { f.fail.candidates = 1 }},
		{"lookup fault", func(f *FaultStore) { f.fail.lookupNew = 1 }},
		{"put fault", func(f *FaultStore) { f.fail.putNew = 1 }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			oldH, newH := legacyHash("alpha", "ALPHA"), strongHash()
			store, oldDigests := seedStore(t, contents, oldH)
			before, _ := store.SnapshotRefs()

			fault := newFaultStore(store)
			tc.fail(fault)
			migrator := NewMigrator(fault, oldH, newH)

			sawInjected := false
			for _, oldDigest := range oldDigests {
				if _, err := migrator.Resolve(oldDigest); errors.Is(err, errInjected) {
					sawInjected = true
				}
			}
			if !sawInjected {
				t.Fatal("expected the injected fault to surface")
			}

			recovered := NewMigrator(store, oldH, newH)
			ambiguousKeys := make(map[string]int)
			for content, oldDigest := range oldDigests {
				if _, err := recovered.Resolve(oldDigest); err != nil {
					var am *AmbiguityError
					if !errors.As(err, &am) {
						t.Fatalf("recovery Resolve failed for %q: %v", content, err)
					}
					ambiguousKeys[string(oldDigest)]++
				}
			}
			if len(ambiguousKeys) != 1 {
				t.Fatalf("distinct ambiguous keys after recovery = %d, want 1", len(ambiguousKeys))
			}

			err := recovered.MigrateAll()
			var am *AmbiguityError
			if !errors.As(err, &am) {
				t.Fatalf("MigrateAll error = %v, want retained ambiguity", err)
			}

			after, newRefs := store.SnapshotRefs()
			if !sameRefMap(before, after) {
				t.Fatalf("old index refs changed during recovery: before=%v after=%v", before, after)
			}
			if sumRefs(before) != sumRefs(newRefs) {
				t.Fatalf("refs not conserved: old=%d new=%d", sumRefs(before), sumRefs(newRefs))
			}
			if store.NewCount() != 3 {
				t.Fatalf("NewCount = %d, want 3 distinct identities", store.NewCount())
			}
		})
	}
}

func TestPutFaultLeavesNoPartialIdentity(t *testing.T) {
	oldH, newH := legacyHash("alpha", "ALPHA"), strongHash()
	store, oldDigests := seedStore(t, map[string]int{"alpha": 2, "ALPHA": 3}, oldH)

	fault := newFaultStore(store)
	fault.fail.putNew = 1
	migrator := NewMigrator(fault, oldH, newH)

	shared := oldDigests["alpha"]
	if _, err := migrator.Resolve(shared); !errors.Is(err, errInjected) {
		t.Fatalf("Resolve error = %v, want injected PutNew fault", err)
	}
	if store.NewCount() != 0 {
		t.Fatalf("NewCount = %d, failed migration must not install a partial identity", store.NewCount())
	}

	recovered := NewMigrator(store, oldH, newH)
	got, err := recovered.Resolve(shared)
	var am *AmbiguityError
	if !errors.As(err, &am) || len(got) != 2 {
		t.Fatalf("recovery = %v %v, want ambiguity with 2 identities", got, err)
	}
	if store.NewCount() != 2 {
		t.Fatalf("NewCount = %d, want both identities after recovery", store.NewCount())
	}
	_, newRefs := store.SnapshotRefs()
	if sumRefs(newRefs) != 5 {
		t.Fatalf("recovered refs = %d, want 5", sumRefs(newRefs))
	}
}

func TestOldDigestTamperIsIntegrityError(t *testing.T) {
	oldH, newH := legacyHash(), strongHash()
	store, oldDigests := seedStore(t, map[string]int{"alpha": 1}, oldH)
	migrator := NewMigrator(store, oldH, newH)

	store.mu.Lock()
	for _, blobs := range store.oldIndex {
		for _, blob := range blobs {
			blob.Content = []byte("tampered")
		}
	}
	store.mu.Unlock()

	_, err := migrator.Resolve(oldDigests["alpha"])
	var integrity *IntegrityError
	if !errors.As(err, &integrity) {
		t.Fatalf("Resolve error = %v, want *IntegrityError", err)
	}
	if store.NewCount() != 0 {
		t.Fatalf("NewCount = %d, tampered content must not establish identity", store.NewCount())
	}
}

func TestMigrateAllCollectsAmbiguityAndFinishesCleanKeys(t *testing.T) {
	oldH, newH := legacyHash("alpha", "ALPHA"), strongHash()
	store, _ := seedStore(t, map[string]int{"alpha": 2, "ALPHA": 4, "beta": 8}, oldH)
	migrator := NewMigrator(store, oldH, newH)

	err := migrator.MigrateAll()
	var am *AmbiguityError
	if !errors.As(err, &am) {
		t.Fatalf("MigrateAll error = %v, want ambiguity joined into result", err)
	}
	if store.NewCount() != 3 {
		t.Fatalf("NewCount = %d, want every distinct object migrated", store.NewCount())
	}
	oldRefs, newRefs := store.SnapshotRefs()
	if sumRefs(oldRefs) != sumRefs(newRefs) {
		t.Fatalf("refs not conserved: old=%v new=%v", oldRefs, newRefs)
	}
}

func TestConcurrentMigrationAndReads(t *testing.T) {
	oldH, newH := legacyHash("alpha", "ALPHA", "gamma"), strongHash()
	contents := map[string]int{"alpha": 2, "ALPHA": 5, "gamma": 7, "beta": 11}
	store, oldDigests := seedStore(t, contents, oldH)
	migrator := NewMigrator(store, oldH, newH)

	keys := make([]Digest, 0, len(oldDigests))
	for _, oldDigest := range oldDigests {
		keys = append(keys, oldDigest)
	}

	var wg sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for round := 0; round < 25; round++ {
				key := keys[(worker+round)%len(keys)]
				got, err := migrator.Resolve(key)
				if err != nil {
					var am *AmbiguityError
					if !errors.As(err, &am) {
						t.Errorf("unexpected Resolve error: %v", err)
						return
					}
				}
				for _, newDigest := range got {
					blob, lookupErr := store.LookupNew(newDigest)
					if lookupErr != nil || blob == nil {
						t.Errorf("resolved identity %x not readable: %v", newDigest, lookupErr)
						return
					}
				}
			}
		}(worker)
	}
	wg.Wait()

	if store.NewCount() != 4 {
		t.Fatalf("NewCount = %d, want 4 distinct identities", store.NewCount())
	}
	oldRefs, newRefs := store.SnapshotRefs()
	if sumRefs(oldRefs) != sumRefs(newRefs) {
		t.Fatalf("refs not conserved under concurrency: old=%v new=%v", oldRefs, newRefs)
	}
	if sumRefs(newRefs) != 25 {
		t.Fatalf("total refs = %d, want 25", sumRefs(newRefs))
	}
}

func TestConcurrentSameKeyMigratesOnce(t *testing.T) {
	oldH, newH := legacyHash(), strongHash()
	store, oldDigests := seedStore(t, map[string]int{"alpha": 3}, oldH)

	fault := newFaultStore(store)
	migrator := NewMigrator(fault, oldH, newH)

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := migrator.Resolve(oldDigests["alpha"])
			if err != nil || len(got) != 1 {
				t.Errorf("Resolve = %v %v", got, err)
			}
		}()
	}
	wg.Wait()

	if store.NewCount() != 1 {
		t.Fatalf("NewCount = %d, want exactly one identity under contention", store.NewCount())
	}
	if fault.calls.putNew != 1 {
		t.Fatalf("PutNew calls = %d, want 1 due to per-old-digest serialization", fault.calls.putNew)
	}
	if fault.calls.candidates != 32 {
		t.Fatalf("Candidates calls = %d, want one serialized read per Resolve", fault.calls.candidates)
	}
}

// collidingNewHash forces two distinct contents onto one new digest, which a
// correct migration must reject as an integrity failure rather than merge.
type collidingNewHash struct{}

func (collidingNewHash) Sum([]byte) Digest { return Digest{0x09, 0x09} }

func TestNewDigestCollisionIsIntegrityError(t *testing.T) {
	oldH, newH := legacyHash("alpha", "ALPHA"), (collidingNewHash{}).Sum
	store, oldDigests := seedStore(t, map[string]int{"alpha": 2, "ALPHA": 3}, oldH)
	migrator := NewMigrator(store, oldH, newH)

	_, err := migrator.Resolve(oldDigests["alpha"])
	var integrity *IntegrityError
	if !errors.As(err, &integrity) {
		t.Fatalf("Resolve error = %v, want *IntegrityError for new digest collision", err)
	}
}

func TestMemoryStoreRejectsDistinctContentOnSameNewDigest(t *testing.T) {
	store := NewMemoryStore()
	first := &Blob{Old: Digest("o1"), NewDigest: Digest("same"), Content: []byte("a"), Refs: 1}
	second := &Blob{Old: Digest("o2"), NewDigest: Digest("same"), Content: []byte("b"), Refs: 2}
	if err := store.PutNew(first); err != nil {
		t.Fatalf("first PutNew failed: %v", err)
	}
	err := store.PutNew(second)
	var integrity *IntegrityError
	if !errors.As(err, &integrity) {
		t.Fatalf("second PutNew error = %v, want *IntegrityError", err)
	}

	if err := store.PutNew(first); err != nil {
		t.Fatalf("idempotent PutNew failed: %v", err)
	}
	if store.NewCount() != 1 {
		t.Fatalf("NewCount = %d, want 1", store.NewCount())
	}
	if got := store.OldCount(); got != 0 {
		t.Fatalf("OldCount = %d, want 0 for empty legacy index", got)
	}
	if len(store.SnapshotNew()) != 1 {
		t.Fatal("SnapshotNew must return the installed identity")
	}
}

func TestMigrateAllSurfacesRangeFault(t *testing.T) {
	oldH, newH := legacyHash(), strongHash()
	store, _ := seedStore(t, map[string]int{"alpha": 1}, oldH)
	fault := newFaultStore(store)
	fault.fail.rangeOld = 1
	migrator := NewMigrator(fault, oldH, newH)

	if err := migrator.MigrateAll(); !errors.Is(err, errInjected) {
		t.Fatalf("MigrateAll error = %v, want injected range fault", err)
	}
}

func TestErrorStringsAndDigestEquality(t *testing.T) {
	ambiguous := &AmbiguityError{OldDigest: Digest("old"), Candidates: []Digest{Digest("a"), Digest("b")}}
	if ambiguous.Error() == "" {
		t.Fatal("AmbiguityError must render a message")
	}
	integrity := &IntegrityError{OldDigest: Digest("old"), field: "old digest mismatch"}
	if integrity.Error() == "" {
		t.Fatal("IntegrityError must render a message")
	}
	if !(Digest("x")).Equal(Digest("x")) || (Digest("x")).Equal(Digest("y")) {
		t.Fatal("Digest.Equal has wrong comparison semantics")
	}
	if !equalBytes([]byte("ab"), []byte("ab")) || equalBytes([]byte("ab"), []byte("ac")) || equalBytes([]byte("ab"), []byte("a")) {
		t.Fatal("equalBytes has wrong comparison semantics")
	}
}

package dedup

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// putRooted commits contents in one transaction under a single root.
func putRooted(t *testing.T, s *Store, root string, contents ...[]byte) []BlockKey {
	t.Helper()
	tx := s.Begin()
	keys := make([]BlockKey, 0, len(contents))
	for _, c := range contents {
		keys = append(keys, tx.Put(c))
	}
	tx.SetObject(root, keys)
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit %s: %v", root, err)
	}
	return keys
}

func mustRefCount(t *testing.T, s *Store, key BlockKey, want int) {
	t.Helper()
	got, ok := s.RefCount(key)
	if !ok {
		t.Fatalf("RefCount(%v): block missing", key)
	}
	if got != want {
		t.Fatalf("RefCount(%v) = %d, want %d", key, got, want)
	}
}

func TestDedupSameContentSameKey(t *testing.T) {
	s := NewStore(nil)
	k1 := putRooted(t, s, "a", []byte("hello"))
	k2 := putRooted(t, s, "b", []byte("hello"))
	if k1[0] != k2[0] {
		t.Fatalf("identical content got different keys: %v vs %v", k1[0], k2[0])
	}
	mustRefCount(t, s, k1[0], 2)

	got, ok := s.Get(k1[0])
	if !ok || string(got) != "hello" {
		t.Fatalf("Get = %q, %v", got, ok)
	}

	tx := s.Begin()
	tx.DeleteObject("a")
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	mustRefCount(t, s, k1[0], 1)
}

func TestWeakBucketThenStrongAndLengthConfirm(t *testing.T) {
	// Force every block into the same weak bucket; dedup must still be
	// decided by strong digest + length + content.
	s := NewStore(&Options{
		WeakHash: func([]byte) WeakDigest { return 42 },
	})
	a := putRooted(t, s, "a", []byte("alpha"))
	b := putRooted(t, s, "b", []byte("beta"))
	c := putRooted(t, s, "c", []byte("alpha"))
	if a[0] == b[0] {
		t.Fatal("different content collapsed to one key")
	}
	if a[0] != c[0] {
		t.Fatal("same content not deduplicated")
	}
	mustRefCount(t, s, a[0], 2)
	mustRefCount(t, s, b[0], 1)
}

func TestStrongCollisionFaultIsolated(t *testing.T) {
	// Fault injection: the strong hash always returns the same digest.
	// Different contents must be isolated as variants, never overwritten.
	s := NewStore(&Options{
		StrongHash: func([]byte) StrongDigest { return StrongDigest{0xde, 0xad} },
	})
	kHello := putRooted(t, s, "hello", []byte("hello"))
	kLonger := putRooted(t, s, "longer", []byte("hello-world")) // same digest, different length
	kSame5 := putRooted(t, s, "same5", []byte("HELLO"))         // same digest AND length, different bytes

	if kHello[0].Digest != kLonger[0].Digest || kHello[0].Digest != kSame5[0].Digest {
		t.Fatal("fault injection did not produce a shared digest")
	}
	variants := map[int]bool{kHello[0].Variant: true, kLonger[0].Variant: true, kSame5[0].Variant: true}
	if len(variants) != 3 {
		t.Fatalf("expected 3 isolated variants, got keys %v %v %v", kHello, kLonger, kSame5)
	}

	for root, want := range map[string]string{"hello": "hello", "longer": "hello-world", "same5": "HELLO"} {
		keys, ok := s.Object(root)
		if !ok || len(keys) != 1 {
			t.Fatalf("root %s missing", root)
		}
		got, ok := s.Get(keys[0])
		if !ok || string(got) != want {
			t.Fatalf("root %s: Get = %q, %v; want %q", root, got, ok, want)
		}
		mustRefCount(t, s, keys[0], 1)
	}

	// Deleting one variant must not disturb the others.
	tx := s.Begin()
	tx.DeleteObject("longer")
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if n := s.GC(); n != 1 {
		t.Fatalf("GC removed %d blocks, want 1", n)
	}
	if _, ok := s.Get(kLonger[0]); ok {
		t.Fatal("collected variant still readable")
	}
	for _, k := range []BlockKey{kHello[0], kSame5[0]} {
		if _, ok := s.Get(k); !ok {
			t.Fatalf("surviving variant %v lost after sibling GC", k)
		}
		mustRefCount(t, s, k, 1)
	}

	// Re-putting surviving content still dedups to the same variant.
	kAgain := putRooted(t, s, "again", []byte("HELLO"))
	if kAgain[0] != kSame5[0] {
		t.Fatalf("re-put got %v, want %v", kAgain[0], kSame5[0])
	}
	mustRefCount(t, s, kSame5[0], 2)
}

func TestDuplicateCommitRejected(t *testing.T) {
	s := NewStore(nil)
	tx := s.Begin()
	key := tx.Put([]byte("once"))
	tx.SetObject("r", []BlockKey{key})
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != ErrTxClosed {
		t.Fatalf("second commit = %v, want ErrTxClosed", err)
	}
	mustRefCount(t, s, key, 1) // not double-applied

	// Rollback then commit is also rejected.
	tx2 := s.Begin()
	tx2.Put([]byte("twice"))
	tx2.Rollback()
	if err := tx2.Commit(); err != ErrTxClosed {
		t.Fatalf("commit after rollback = %v, want ErrTxClosed", err)
	}
	if _, ok := s.Get(BlockKey{Digest: DefaultStrongHash([]byte("twice"))}); ok {
		t.Fatal("rolled-back block was stored")
	}

	// Re-setting the same root to the same keys is idempotent.
	putRooted(t, s, "r", []byte("once"))
	mustRefCount(t, s, key, 1)
}

func TestNegativeRefCountGuardAndRollback(t *testing.T) {
	s := NewStore(nil)
	keys := putRooted(t, s, "r", []byte("guarded"))
	key := keys[0]

	// Inject corruption: zero the count while the root still references it.
	s.mu.Lock()
	s.families[key.Digest].variants[key.Variant].refCount = 0
	s.mu.Unlock()

	tx := s.Begin()
	tx.DeleteObject("r")
	err := tx.Commit()
	if err == nil {
		t.Fatal("commit succeeded despite negative count")
	}
	if e := AsError(err); e == nil || e.Err != ErrNegativeRefCount {
		t.Fatalf("err = %v, want ErrNegativeRefCount", err)
	}

	// Rollback: root and (corrupted) count are untouched.
	if _, ok := s.Object("r"); !ok {
		t.Fatal("failed commit removed the root")
	}
	mustRefCount(t, s, key, 0)

	// Rebuild repairs the drift, then the delete succeeds.
	cs := s.RebuildRefCounts()
	if len(cs) != 1 || cs[0].Key != key || cs[0].OldCount != 0 || cs[0].NewCount != 1 {
		t.Fatalf("corrections = %+v", cs)
	}
	mustRefCount(t, s, key, 1)

	tx2 := s.Begin()
	tx2.DeleteObject("r")
	if err := tx2.Commit(); err != nil {
		t.Fatal(err)
	}
	mustRefCount(t, s, key, 0)
	if n := s.GC(); n != 1 {
		t.Fatalf("GC removed %d, want 1", n)
	}
	if _, ok := s.Get(key); ok {
		t.Fatal("block still readable after GC")
	}
}

func TestUnknownBlockRejected(t *testing.T) {
	s := NewStore(nil)
	ghost := BlockKey{Digest: StrongDigest{0x01}, Variant: 0}
	tx := s.Begin()
	tx.SetObject("bad", []BlockKey{ghost})
	err := tx.Commit()
	if e := AsError(err); e == nil || e.Err != ErrUnknownBlock {
		t.Fatalf("err = %v, want ErrUnknownBlock", err)
	}
	if _, ok := s.Object("bad"); ok {
		t.Fatal("failed commit created a root")
	}
}

func TestRebuildRefCountsFromRoots(t *testing.T) {
	s := NewStore(nil)
	k1 := putRooted(t, s, "a", []byte("one"))
	k2 := putRooted(t, s, "b", []byte("two"), []byte("three"))

	// Corrupt two counts; leave one correct.
	s.mu.Lock()
	s.families[k1[0].Digest].variants[k1[0].Variant].refCount = 99
	s.families[k2[0].Digest].variants[k2[0].Variant].refCount = 0
	s.mu.Unlock()

	cs := s.RebuildRefCounts()
	if len(cs) != 2 {
		t.Fatalf("corrections = %+v, want 2 entries", cs)
	}
	mustRefCount(t, s, k1[0], 1)
	mustRefCount(t, s, k2[0], 1)
	mustRefCount(t, s, k2[1], 1)
	if cs := s.RebuildRefCounts(); len(cs) != 0 {
		t.Fatalf("second rebuild still drifting: %+v", cs)
	}
}

func TestGCDeleteVersusNewReferenceRace(t *testing.T) {
	s := NewStore(nil)
	data := []byte("contended-block-content")
	const workers = 8

	for i := 0; i < 100; i++ {
		// Stage an unreferenced copy for GC to hunt.
		tx := s.Begin()
		tx.Put(data)
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}

		keys := make([]BlockKey, workers)
		errs := make(chan error, workers)
		var wg sync.WaitGroup
		for g := 0; g < workers; g++ {
			wg.Add(1)
			go func(g int) {
				defer wg.Done()
				tx := s.Begin()
				k := tx.Put(data)
				tx.SetObject(fmt.Sprintf("root-%d-%d", i, g), []BlockKey{k})
				if err := tx.Commit(); err != nil {
					errs <- err
					return
				}
				keys[g] = k
			}(g)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.GC()
		}()
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatal(err)
		}

		// Every key handed out must be readable with the right content.
		for _, k := range keys {
			got, ok := s.Get(k)
			if !ok || !bytes.Equal(got, data) {
				t.Fatalf("iter %d: Get(%v) = %q, %v", i, k, got, ok)
			}
		}

		// Clean up roots and reclaim for the next round.
		tx = s.Begin()
		for g := 0; g < workers; g++ {
			tx.DeleteObject(fmt.Sprintf("root-%d-%d", i, g))
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		s.GC()
	}

	if cs := s.RebuildRefCounts(); len(cs) != 0 {
		t.Fatalf("reference count drift after race: %+v", cs)
	}
}

func TestConcurrentDuplicatePuts(t *testing.T) {
	s := NewStore(nil)
	data := []byte("shared")
	const workers = 16
	keys := make([]BlockKey, workers)
	var wg sync.WaitGroup
	for g := 0; g < workers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			tx := s.Begin()
			k := tx.Put(data)
			tx.SetObject(fmt.Sprintf("r%d", g), []BlockKey{k})
			if err := tx.Commit(); err != nil {
				t.Errorf("commit: %v", err)
			}
			keys[g] = k
		}(g)
	}
	wg.Wait()
	for _, k := range keys {
		if k != keys[0] {
			t.Fatalf("concurrent puts diverged: %v vs %v", k, keys[0])
		}
	}
	mustRefCount(t, s, keys[0], workers)
	if cs := s.RebuildRefCounts(); len(cs) != 0 {
		t.Fatalf("drift: %+v", cs)
	}
}

func TestErrorMessageMentionsKey(t *testing.T) {
	s := NewStore(nil)
	ghost := BlockKey{Digest: StrongDigest{0x0f}, Variant: 3}
	tx := s.Begin()
	tx.SetObject("bad", []BlockKey{ghost})
	err := tx.Commit()
	if err == nil || !strings.Contains(err.Error(), ghost.String()) {
		t.Fatalf("error %v does not identify the block", err)
	}
}

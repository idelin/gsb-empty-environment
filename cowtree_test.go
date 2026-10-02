package cowtree

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
)

// TestPutGetRandom inserts keys in random order and verifies the tree
// matches a reference map, exercising many leaf/internal splits.
func TestPutGetRandom(t *testing.T) {
	tr := New()
	ref := map[int]string{}
	keys := rand.Perm(2000)
	for _, k := range keys {
		v := fmt.Sprintf("v%d", k)
		tr.Put(k, v)
		ref[k] = v
	}
	snap := tr.Snapshot()
	if got := snap.Count(); got != len(ref) {
		t.Fatalf("count = %d, want %d", got, len(ref))
	}
	for k, want := range ref {
		got, ok := snap.Get(k)
		if !ok || got != want {
			t.Fatalf("Get(%d) = %q,%v; want %q,true", k, got, ok, want)
		}
	}
	// Scan must be fully sorted and complete.
	gotKeys, _ := snap.Collect()
	if !sort.IntsAreSorted(gotKeys) || len(gotKeys) != len(ref) {
		t.Fatalf("scan not sorted/complete: len=%d", len(gotKeys))
	}
	for i, k := range gotKeys {
		if k != i {
			t.Fatalf("scan[%d] = %d, want %d", i, k, i)
		}
	}
}

// TestOverwrite verifies CoW overwrite semantics.
func TestOverwrite(t *testing.T) {
	tr := New()
	tr.Put(1, "old")
	tr.Put(2, "two")
	before := tr.Snapshot()
	tr.Put(1, "new")
	if v, _ := before.Get(1); v != "old" {
		t.Fatalf("snapshot before overwrite sees %q, want %q", v, "old")
	}
	if v, _ := tr.Snapshot().Get(1); v != "new" {
		t.Fatalf("latest sees %q, want %q", v, "new")
	}
}

// TestSnapshotIsolation verifies that a snapshot taken before a burst of
// writes (inserts + overwrites, causing many splits) keeps its exact view.
func TestSnapshotIsolation(t *testing.T) {
	tr := New()
	const base = 500
	for i := 0; i < base; i++ {
		tr.Put(i, fmt.Sprintf("v%d", i))
	}
	snap := tr.Snapshot()
	wantKeys, wantVals := snap.Collect()

	// Heavy write activity: overwrite everything and insert many new keys.
	for i := 0; i < base; i++ {
		tr.Put(i, "overwritten")
	}
	for i := base; i < base*4; i++ {
		tr.Put(i, fmt.Sprintf("v%d", i))
	}

	gotKeys, gotVals := snap.Collect()
	if !reflect.DeepEqual(gotKeys, wantKeys) || !reflect.DeepEqual(gotVals, wantVals) {
		t.Fatal("snapshot view changed after concurrent-version writes")
	}
	if v, _ := snap.Get(0); v != "v0" {
		t.Fatalf("snapshot Get(0) = %q, want v0", v)
	}
	latest := tr.Snapshot()
	if latest.Count() != base*4 {
		t.Fatalf("latest count = %d, want %d", latest.Count(), base*4)
	}
	if v, _ := latest.Get(0); v != "overwritten" {
		t.Fatalf("latest Get(0) = %q, want overwritten", v)
	}
}

// TestConcurrentSplitScan is the acceptance test: while a writer inserts
// thousands of keys (constantly splitting pages and swapping roots), readers
// holding the pre-write root must scan the exact same consistent snapshot
// view on every pass, with no torn reads, no missing/extra keys, and no
// data races (run with -race).
func TestConcurrentSplitScan(t *testing.T) {
	tr := New()
	const base = 1000
	for i := 0; i < base; i++ {
		tr.Put(i, fmt.Sprintf("v%d", i))
	}

	snap := tr.Snapshot() // read-only transaction pins the old root
	wantKeys, wantVals := snap.Collect()
	if len(wantKeys) != base {
		t.Fatalf("setup: snapshot has %d keys, want %d", len(wantKeys), base)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Writer: inserts keys base..base+19999, forcing continuous splits.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := base; i < base+20000; i++ {
			tr.Put(i, fmt.Sprintf("v%d", i))
		}
		close(stop)
	}()

	// Readers: scan the pinned snapshot until the writer finishes.
	const numReaders = 4
	errCh := make(chan string, numReaders)
	for r := 0; r < numReaders; r++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			passes := 0
			for {
				select {
				case <-stop:
					if passes == 0 {
						errCh <- fmt.Sprintf("reader %d: no scan passes completed", id)
					}
					return
				default:
				}
				idx := 0
				snap.Scan(func(k int, v string) bool {
					if idx >= len(wantKeys) {
						errCh <- fmt.Sprintf("reader %d: extra key %d in snapshot", id, k)
						return false
					}
					if k != wantKeys[idx] || v != wantVals[idx] {
						errCh <- fmt.Sprintf("reader %d: scan[%d] = (%d,%q), want (%d,%q)",
							id, idx, k, v, wantKeys[idx], wantVals[idx])
						return false
					}
					idx++
					return true
				})
				if idx != len(wantKeys) {
					errCh <- fmt.Sprintf("reader %d: scanned %d keys, want %d", id, idx, len(wantKeys))
				}
				passes++
			}
		}(r)
	}
	wg.Wait()
	close(errCh)
	for msg := range errCh {
		t.Error(msg)
	}

	// After the writer finishes, a fresh snapshot sees everything.
	if got := tr.Snapshot().Count(); got != base+20000 {
		t.Fatalf("final count = %d, want %d", got, base+20000)
	}
	// And the old snapshot is STILL intact.
	if got := snap.Count(); got != base {
		t.Fatalf("old snapshot count = %d, want %d", got, base)
	}
}

// TestSnapshotSeesMonotonicVersions checks that snapshots taken between
// writes observe exactly the writes committed before them.
func TestSnapshotSeesMonotonicVersions(t *testing.T) {
	tr := New()
	var snaps []*Snapshot
	for i := 0; i < 100; i++ {
		tr.Put(i, fmt.Sprintf("v%d", i))
		snaps = append(snaps, tr.Snapshot())
	}
	for i, s := range snaps {
		if got := s.Count(); got != i+1 {
			t.Fatalf("snapshot %d count = %d, want %d", i, got, i+1)
		}
		if _, ok := s.Get(i + 1); ok && i+1 < 100 {
			t.Fatalf("snapshot %d unexpectedly sees key %d", i, i+1)
		}
	}
}

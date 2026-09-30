package sstdict

import (
	"sort"
	"strings"
	"testing"
)

// mergedReference is the independent ground truth: sorted unique union.
func mergedReference(batches ...[]string) []string {
	set := make(map[string]struct{})
	for _, b := range batches {
		for _, w := range b {
			set[w] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for w := range set {
		out = append(out, w)
	}
	sort.Strings(out)
	return out
}

// TestBatchUpdatesAndSnapshots covers: immutable segments per batch,
// multi-segment queries against an independent array, merge into one
// segment, old snapshot readability, and reclamation conditions.
func TestBatchUpdatesAndSnapshots(t *testing.T) {
	store := NewStore()
	batch1 := []string{"apple", "banana", "cherry", "中文一"}
	batch2 := []string{"apple", "apricot", "blueberry", "中文二"} // "apple" duplicated across batches
	batch3 := []string{"avocado", "cherry"}
	if err := store.AddBatch(batch1, 4); err != nil {
		t.Fatal(err)
	}
	if err := store.AddBatch(batch2, 4); err != nil {
		t.Fatal(err)
	}

	// Snapshot pinning two segments; queries must match merged reference.
	snap1 := store.Snapshot()
	ref12 := mergedReference(batch1, batch2)
	n, err := snap1.Len()
	if err != nil || n != uint64(len(ref12)) {
		t.Fatalf("Len = %d, %v; want %d", n, err, len(ref12))
	}
	for k, want := range ref12 {
		got, err := snap1.Get(uint64(k), nil)
		if err != nil || got != want {
			t.Fatalf("multi-segment Get(%d) = %q, %v; want %q", k, got, err, want)
		}
	}
	gotRange, err := snap1.PrefixRange("中文", nil)
	if err != nil || strings.Join(gotRange, ",") != "中文一,中文二" {
		t.Fatalf("multi-segment PrefixRange = %v, %v", gotRange, err)
	}

	// Third batch, then merge everything into one immutable segment.
	if err := store.AddBatch(batch3, 4); err != nil {
		t.Fatal(err)
	}
	if err := store.Merge(4); err != nil {
		t.Fatal(err)
	}

	st := store.Stats()
	if st.LiveSegments != 1 {
		t.Fatalf("LiveSegments = %d, want 1", st.LiveSegments)
	}
	// Reclamation condition: batch3 was never pinned by snap1, so it is
	// reclaimed immediately; batch1/batch2 stay alive while snap1 is open.
	if st.ReclaimedSegments != 1 {
		t.Fatalf("ReclaimedSegments = %d, want 1 (only the unpinned batch3 segment)", st.ReclaimedSegments)
	}

	// Old snapshot still reads the pre-merge view (without batch3).
	if n, _ := snap1.Len(); n != uint64(len(ref12)) {
		t.Fatalf("old snapshot Len = %d, want %d", n, len(ref12))
	}
	if w, _ := snap1.Get(0, nil); w != "apple" {
		t.Fatalf("old snapshot Get(0) = %q, want apple", w)
	}

	// New snapshot sees the merged view.
	snap2 := store.Snapshot()
	ref123 := mergedReference(batch1, batch2, batch3)
	if n, _ := snap2.Len(); n != uint64(len(ref123)) {
		t.Fatalf("merged Len = %d, want %d", n, len(ref123))
	}
	var st2 Stats
	for k, want := range ref123 {
		got, err := snap2.Get(uint64(k), &st2)
		if err != nil || got != want {
			t.Fatalf("merged Get(%d) = %q, %v; want %q", k, got, err, want)
		}
	}
	if st2.MaxDecodedPerRead > 4 {
		t.Fatalf("merged single-segment Get decoded %d entries, block size 4", st2.MaxDecodedPerRead)
	}

	// Closing the old snapshot drops the last pins: the two pinned
	// segments become reclaimable (superseded AND refcount zero).
	snap1.Close()
	st = store.Stats()
	if st.ReclaimedSegments != 3 {
		t.Fatalf("ReclaimedSegments = %d after closing snapshot, want 3", st.ReclaimedSegments)
	}
	if st.ReclaimedBytes == 0 {
		t.Fatal("ReclaimedBytes should be positive after reclamation")
	}
	t.Logf("store stats after merge+close: %+v", st)
	snap2.Close()
}

// TestMergeWithoutSnapshotReclaimsImmediately: with no open snapshot,
// superseded segments are reclaimed at merge time.
func TestMergeWithoutSnapshotReclaimsImmediately(t *testing.T) {
	store := NewStore()
	store.AddBatch([]string{"a", "b"}, 2)
	store.AddBatch([]string{"c", "d"}, 2)
	if err := store.Merge(2); err != nil {
		t.Fatal(err)
	}
	st := store.Stats()
	if st.ReclaimedSegments != 2 || st.LiveSegments != 1 {
		t.Fatalf("stats = %+v, want 2 reclaimed and 1 live", st)
	}
	snap := store.Snapshot()
	defer snap.Close()
	for k, want := range []string{"a", "b", "c", "d"} {
		got, err := snap.Get(uint64(k), nil)
		if err != nil || got != want {
			t.Fatalf("Get(%d) = %q, %v; want %q", k, got, err, want)
		}
	}
}

// TestDuplicateBatchRejected: duplicates inside one batch are an error.
func TestDuplicateBatchRejected(t *testing.T) {
	store := NewStore()
	if err := store.AddBatch([]string{"a", "a"}, 2); err == nil {
		t.Fatal("expected duplicate error for batch with repeated word")
	}
}

// TestSnapshotIsolationFromCorruption is a sanity check that segments are
// immutable: a snapshot taken before a later batch keeps its word count.
func TestSnapshotIsolation(t *testing.T) {
	store := NewStore()
	store.AddBatch([]string{"a", "b", "c"}, 2)
	snap := store.Snapshot()
	defer snap.Close()
	store.AddBatch([]string{"d", "e"}, 2)
	if n, _ := snap.Len(); n != 3 {
		t.Fatalf("snapshot Len = %d after later batch, want 3", n)
	}
}

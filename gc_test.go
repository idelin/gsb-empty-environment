package bptree

import "testing"

// Reclamation frees exactly the pages unreachable from every active root:
// pages pinned by an open snapshot survive, everything else is recycled.
func TestCollectKeepsOnlyActiveRoots(t *testing.T) {
	tr := mustTree(t, 4)
	fill(t, tr, 0, 60)

	snap := tr.Snapshot()
	defer snap.Close()

	// Replace the whole contents: the new version shares nothing with most
	// of the old pages, but the snapshot pins its own version.
	tx := tr.Begin()
	for k := int64(0); k < 60; k++ {
		if err := tx.Delete(k); err != nil {
			t.Fatalf("Delete: %v", err)
		}
	}
	for k := int64(1000); k < 1060; k++ {
		if err := tx.Put(k, []byte("new")); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	freed, err := tr.Collect()
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if freed == 0 {
		t.Fatalf("Collect freed nothing")
	}

	// The snapshot's pinned version is fully intact after reclamation.
	keys, _ := drain(t, snap.Iterator(-1, 100))
	if len(keys) != 60 {
		t.Fatalf("snapshot scan after GC yielded %d keys, want 60", len(keys))
	}
	for i, k := range keys {
		if k != int64(i) {
			t.Fatalf("snapshot key %d = %d after GC", i, k)
		}
	}
	if err := tr.Validate(); err != nil {
		t.Fatalf("Validate after GC: %v", err)
	}

	// After the snapshot closes, its pages become reclaimable too.
	snap.Close()
	before := tr.Store().AllocatedCount()
	freed2, err := tr.Collect()
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if freed2 == 0 {
		t.Fatalf("second Collect freed nothing")
	}
	if after := tr.Store().AllocatedCount(); after >= before {
		t.Fatalf("allocated pages did not shrink: before=%d after=%d", before, after)
	}
	if err := tr.Validate(); err != nil {
		t.Fatalf("Validate after second GC: %v", err)
	}
	keys, _ = drain(t, tr.Snapshot().Iterator(999, 1061))
	if len(keys) != 60 {
		t.Fatalf("live scan after GC yielded %d keys, want 60", len(keys))
	}
}

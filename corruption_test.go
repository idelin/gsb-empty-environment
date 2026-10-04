package bptree

import "testing"

// Flipping a byte in the page file is detected both by the integrity check
// and by any read of the damaged page.
func TestPageFileCorruptionDetection(t *testing.T) {
	tr := mustTree(t, 4)
	fill(t, tr, 0, 80)

	// Corrupt the root page: every read path crosses it.
	target := tr.Root()
	tr.Store().Corrupt(target)

	bad := tr.Store().CheckIntegrity()
	if len(bad) != 1 || bad[0] != target {
		t.Fatalf("CheckIntegrity = %v, want [%d]", bad, target)
	}
	if _, err := tr.Store().Read(target); err != nil {
		// Read itself does not verify; decoding does.
		t.Fatalf("Read should succeed at store level, got %v", err)
	}
	if _, err := tr.Get(40); err == nil {
		t.Fatalf("Get through corrupt page should fail")
	}
	if err := tr.Validate(); err == nil {
		t.Fatalf("Validate should fail on corrupt page")
	}
}

// Corruption in an unreachable page is reported by the integrity check but
// does not affect the live tree.
func TestCorruptionInGarbagePage(t *testing.T) {
	tr := mustTree(t, 4)
	fill(t, tr, 0, 40)
	snap := tr.Snapshot() // pin old pages so GC does not reclaim them
	defer snap.Close()
	fill(t, tr, 100, 150)

	var garbage PageID
	found := false
	for _, id := range tr.Store().Allocated() {
		garbage = id
		found = true
	}
	if !found {
		t.Fatalf("no pages allocated")
	}
	// Corrupt the null-page neighbor: allocate then free a page, then corrupt
	// is skipped for freed pages, so instead corrupt the highest allocated
	// page only if unreachable. Simplest: corrupt a fresh unreachable page.
	p := tr.Store().Alloc()
	junk := make([]byte, tr.Store().PageSize())
	copy(junk, []byte("not a b+tree page at all"))
	if err := tr.Store().Write(p, junk); err != nil {
		t.Fatalf("Write: %v", err)
	}
	_ = garbage

	bad := tr.Store().CheckIntegrity()
	if len(bad) != 1 || bad[0] != p {
		t.Fatalf("CheckIntegrity = %v, want [%d]", bad, p)
	}
	if err := tr.Validate(); err != nil {
		t.Fatalf("Validate should ignore unreachable corrupt page: %v", err)
	}
}

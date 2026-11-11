package pagetable

import "testing"

// TestSplitPreservesOutside splits a 2 MiB page for one inner 4 KiB patch and
// verifies that every untouched 4 KiB page keeps its address and permissions,
// raw PTE bits round-trip, and walk results outside the modified area match
// the old snapshot exactly.
func TestSplitPreservesOutside(t *testing.T) {
	b := newBuilder(t)
	const va = uint64(0x40000000)
	const phys = uint64(0x80000000)
	origPerm := Perm{Write: true, User: true, Execute: false}
	b.map2M(va, phys, origPerm)

	old := b.snap(b.root)

	svc := NewService(b.store, b.alloc, b.root)
	patchVA := va + 3*PageSize
	newPerm := Perm{Write: false, User: true, Execute: false}
	if _, err := svc.Update(Patch{Start: patchVA, End: patchVA + PageSize, Perm: newPerm}); err != nil {
		t.Fatalf("update: %v", err)
	}

	cur := svc.Current()
	mustPerm(t, cur, patchVA, newPerm, phys+3*PageSize, PageSize)

	// All other 511 pages are unchanged, down to the raw leaf entry.
	for i := uint64(0); i < 512; i++ {
		addr := va + i*PageSize
		if i == 3 {
			continue
		}
		m, err := cur.Lookup(addr)
		if err != nil {
			t.Fatalf("lookup %d: %v", i, err)
		}
		if m.Phys != phys+i*PageSize || m.Size != PageSize || m.Perm != origPerm {
			t.Fatalf("page %d changed unexpectedly: %+v", i, m)
		}
		newRaw, _, _, _, err := cur.LeafEntry(addr)
		if err != nil {
			t.Fatalf("new leaf %d: %v", i, err)
		}
		// The untouched PTE must preserve frame, presence and all flags that
		// encode the original effective permissions.
		wantRaw := flagPresent | (phys + i*PageSize) | flagRW | flagUS | flagNX
		if newRaw != wantRaw {
			t.Fatalf("page %d raw PTE = %#x, want %#x", i, newRaw, wantRaw)
		}
	}
	// The old snapshot still resolves the whole region as one 2 MiB leaf.
	oldLeaf, _, _, _, err := old.LeafEntry(va)
	if err != nil {
		t.Fatalf("old leaf: %v", err)
	}
	if oldLeaf&flagPS == 0 {
		t.Fatalf("old snapshot lost its 2 MiB leaf: %#x", oldLeaf)
	}

	// Whole-tree mapping enumeration agrees outside the patch.
	oldMaps, err := old.Mappings()
	if err != nil {
		t.Fatal(err)
	}
	newMaps, err := cur.Mappings()
	if err != nil {
		t.Fatal(err)
	}
	if len(oldMaps) != 1 || oldMaps[0].Size != LargePageSize {
		t.Fatalf("old tree expected one 2 MiB mapping, got %+v", oldMaps)
	}
	if len(newMaps) != 512 {
		t.Fatalf("new tree expected 512 4 KiB mappings, got %d", len(newMaps))
	}
	for _, m := range newMaps {
		if m.VA == patchVA {
			continue
		}
		if m.Phys != phys+(m.VA-va) || m.Perm != origPerm {
			t.Fatalf("mapping outside patch differs: %+v", m)
		}
	}

	// Physical coverage is identical: splitting changes frame granularity,
	// not the bytes of backing memory that are reachable.
	oldCover := physicalCoverage(oldMaps)
	newCover := physicalCoverage(newMaps)
	if !intervalsEqual(oldCover, newCover) {
		t.Fatalf("physical coverage changed:\nold=%v\nnew=%v", oldCover, newCover)
	}

	// The old snapshot is still queryable and untouched.
	mustPerm(t, old, va, origPerm, phys, LargePageSize)
}

// TestCrossLargePageBoundary patches a range straddling a 2 MiB boundary.
func TestCrossLargePageBoundary(t *testing.T) {
	b := newBuilder(t)
	const a = uint64(0x20000000)
	const bVA = uint64(0x20200000)
	pa, pb := uint64(0x10000000), uint64(0x20000000)
	p := Perm{Write: true, User: true, Execute: true}
	b.map2M(a, pa, p)
	b.map2M(bVA, pb, p)

	svc := NewService(b.store, b.alloc, b.root)
	start := a + 510*PageSize
	end := bVA + 2*PageSize
	np := Perm{Write: true, User: false, Execute: true}
	if _, err := svc.Update(Patch{Start: start, End: end, Perm: np}); err != nil {
		t.Fatalf("update: %v", err)
	}
	cur := svc.Current()

	mustPerm(t, cur, start, np, pa+510*PageSize, PageSize)
	mustPerm(t, cur, a+511*PageSize, np, pa+511*PageSize, PageSize)
	mustPerm(t, cur, bVA, np, pb, PageSize)
	mustPerm(t, cur, bVA+PageSize, np, pb+PageSize, PageSize)

	// Neighbors outside the range keep original perms.
	mustPerm(t, cur, a+509*PageSize, p, pa+509*PageSize, PageSize)
	mustPerm(t, cur, bVA+2*PageSize, p, pb+2*PageSize, PageSize)
}

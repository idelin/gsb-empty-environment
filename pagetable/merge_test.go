package pagetable

import "testing"

// TestMergeAfterUndo revokes and then restores permissions; after the final
// patch the split PT collapses back into a single 2 MiB leaf with the exact
// raw entry and frame layout of the original.
func TestMergeAfterUndo(t *testing.T) {
	b := newBuilder(t)
	const va = uint64(0x60000000)
	const phys = uint64(0xa0000000)
	orig := Perm{Write: true, User: true, Execute: true}
	b.map2M(va, phys, orig)
	old := b.snap(b.root)
	oldInfo, err := old.Analyze()
	if err != nil {
		t.Fatal(err)
	}

	svc := NewService(b.store, b.alloc, b.root)
	revoked := Perm{Write: false, User: false, Execute: false}
	if _, err := svc.Update(Patch{
		Start: va + 100*PageSize,
		End:   va + 101*PageSize,
		Perm:  revoked,
	}); err != nil {
		t.Fatal(err)
	}
	split := svc.Current()
	if m, _ := split.Lookup(va); m.Size != PageSize {
		t.Fatalf("expected split to 4 KiB, got size %#x", m.Size)
	}

	// Restore the original permissions across the whole 2 MiB region.
	if _, err := svc.Update(Patch{Start: va, End: va + LargePageSize, Perm: orig}); err != nil {
		t.Fatal(err)
	}
	cur := svc.Current()

	mustPerm(t, cur, va, orig, phys, LargePageSize)
	mergedRaw, _, _, m, err := cur.LeafEntry(va)
	if err != nil {
		t.Fatal(err)
	}
	if m.Size != LargePageSize {
		t.Fatalf("expected re-merged 2 MiB leaf, got size %#x", m.Size)
	}
	oldRaw, _, _, _, err := old.LeafEntry(va)
	if err != nil {
		t.Fatal(err)
	}
	if mergedRaw != oldRaw {
		t.Fatalf("merged entry %#x != original %#x", mergedRaw, oldRaw)
	}

	newInfo, err := cur.Analyze()
	if err != nil {
		t.Fatal(err)
	}
	if len(newInfo.TablePages) != len(oldInfo.TablePages) {
		t.Fatalf("table page count %d != original %d",
			len(newInfo.TablePages), len(oldInfo.TablePages))
	}
	oldMaps, _ := old.Mappings()
	newMaps, _ := cur.Mappings()
	if !intervalsEqual(physicalCoverage(oldMaps), physicalCoverage(newMaps)) {
		t.Fatal("physical coverage differs after merge")
	}
}

// TestPartialUndoStaysSplit restores only part of a split page and checks
// that no incorrect merge happens.
func TestPartialUndoStaysSplit(t *testing.T) {
	b := newBuilder(t)
	const va = uint64(0x70000000)
	const phys = uint64(0xb0000000)
	orig := Perm{Write: true, User: true, Execute: false}
	b.map2M(va, phys, orig)
	svc := NewService(b.store, b.alloc, b.root)

	revoked := Perm{Write: false, User: true, Execute: false}
	if _, err := svc.Update(Patch{
		Start: va + 5*PageSize,
		End:   va + 7*PageSize,
		Perm:  revoked,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Update(Patch{
		Start: va + 5*PageSize,
		End:   va + 6*PageSize,
		Perm:  orig,
	}); err != nil {
		t.Fatal(err)
	}
	cur := svc.Current()
	mustPerm(t, cur, va+5*PageSize, orig, phys+5*PageSize, PageSize)
	mustPerm(t, cur, va+6*PageSize, revoked, phys+6*PageSize, PageSize)
	mustPerm(t, cur, va+7*PageSize, orig, phys+7*PageSize, PageSize)
}

// TestSingleTransactionSplitAndCollapse verifies that when a transaction
// splits a 2 MiB page and restores uniform permissions before commit, the
// collapse pass reclaims the PT frame it allocated itself, leaving exactly
// the ancestor table pages the transaction needed to copy.
func TestSingleTransactionSplitAndCollapse(t *testing.T) {
	b := newBuilder(t)
	const va = uint64(0x71000000)
	const phys = uint64(0xb1000000)
	orig := Perm{Write: true, User: true, Execute: true}
	b.map2M(va, phys, orig)

	svc := NewService(b.store, b.alloc, b.root)
	before := b.alloc.liveCount()

	revoked := Perm{Write: false, User: true, Execute: true}
	if _, err := svc.Update(
		Patch{Start: va + 10*PageSize, End: va + 11*PageSize, Perm: revoked},
		Patch{Start: va, End: va + LargePageSize, Perm: orig},
	); err != nil {
		t.Fatal(err)
	}

	// Only PML4, PDPT and PD copies remain live from this transaction; the
	// transient PT was collapsed and returned to the allocator.
	after := b.alloc.liveCount()
	if after != before+3 {
		t.Fatalf("live pages = %d, want %d (3 ancestor copies, PT reclaimed)",
			after, before+3)
	}
	mustPerm(t, svc.Current(), va, orig, phys, LargePageSize)
}

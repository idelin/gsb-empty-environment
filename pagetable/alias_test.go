package pagetable

import "testing"

// TestAliasOnlyNamedPathChanges aliases two virtual regions onto one shared
// PDPT/PD page and confirms that editing one region rewrites only the named
// path; the alias keeps pointing at the untouched shared subtree.
func TestAliasOnlyNamedPathChanges(t *testing.T) {
	b := newBuilder(t)
	const va1 = uint64(0x0000_0000_1000_0000) // PML4 index 0
	const va2 = uint64(0x0000_0080_1000_0000) // PML4 index 1, same low 39 bits
	const phys = uint64(0xc0000000)
	orig := Perm{Write: true, User: true, Execute: true}
	b.map2M(va1, phys, orig)
	b.aliasPML4(va1, va2)

	old := b.snap(b.root)
	oldInfo, err := old.Analyze()
	if err != nil {
		t.Fatal(err)
	}
	sharedPDPT, err := readEntry(b.store, b.root, entryIndex(va1, LevelPML4))
	if err != nil {
		t.Fatal(err)
	}
	sharedPDPT &= tableFrameMask
	if oldInfo.TableRefs[sharedPDPT] != 2 {
		t.Fatalf("expected PDPT refcount 2 before edit")
	}

	svc := NewService(b.store, b.alloc, b.root)
	np := Perm{Write: false, User: true, Execute: true}
	if _, err := svc.Update(Patch{Start: va1, End: va1 + PageSize, Perm: np}); err != nil {
		t.Fatal(err)
	}
	cur := svc.Current()

	// Named path changed.
	mustPerm(t, cur, va1, np, phys, PageSize)
	// Aliased path is completely untouched: still a 2 MiB leaf, old perms.
	mustPerm(t, cur, va2, orig, phys, LargePageSize)

	newInfo, err := cur.Analyze()
	if err != nil {
		t.Fatal(err)
	}
	// Physical backing is preserved; the edited leaf is one PTE inside the
	// same 2 MiB frame, the alias still references the whole large page.
	oldMaps, _ := old.Mappings()
	newMaps, _ := cur.Mappings()
	if !intervalsEqual(physicalCoverage(oldMaps), physicalCoverage(newMaps)) {
		t.Fatal("physical backing coverage changed after aliased edit")
	}

	// The alias path still references the original shared subtree pages;
	// only the edited path was copied.
	aliasPML4Entry, err := readEntry(b.store, cur.Root(), entryIndex(va2, LevelPML4))
	if err != nil {
		t.Fatal(err)
	}
	if aliasPML4Entry&tableFrameMask != sharedPDPT {
		t.Fatalf("alias PML4 entry was rewritten: %#x", aliasPML4Entry)
	}
	if _, stillReachable := newInfo.TablePages[sharedPDPT]; !stillReachable {
		t.Fatal("shared aliased PDPT disappeared from reachable set")
	}

	// Old snapshot remains fully unchanged.
	mustPerm(t, old, va1, orig, phys, LargePageSize)
	mustPerm(t, old, va2, orig, phys, LargePageSize)
}

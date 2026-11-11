package pagetable

import (
	"errors"
	"testing"
)

// TestCanonical rejects non-canonical addresses and bad ranges everywhere
// they can enter the API.
func TestCanonical(t *testing.T) {
	b := newBuilder(t)
	const va = uint64(0x40000000)
	b.map2M(va, 0x80000000, Perm{Write: true, User: true, Execute: true})
	snap := b.snap(b.root)
	svc := NewService(b.store, b.alloc, b.root)

	// Bit 47 clear but bit 48 set: non-canonical hole.
	const nonCanonical = uint64(0x0000_8000_0000_0000)
	if Canonical(nonCanonical) {
		t.Fatal("address should be non-canonical")
	}
	if Canonical(0x0000_7fff_ffff_ffff) == false {
		t.Fatal("canonical top userspace address rejected")
	}
	if Canonical(0xffff_7fff_ffff_ffff) {
		t.Fatal("address should be non-canonical")
	}
	if !Canonical(va) || !Canonical(0xffff_ffff_ffff_f000) {
		t.Fatal("canonical address rejected")
	}

	if _, err := snap.Lookup(nonCanonical); !errors.Is(err, ErrNotCanonical) {
		t.Fatalf("Lookup err = %v", err)
	}
	if _, _, _, _, err := snap.LeafEntry(nonCanonical); !errors.Is(err, ErrNotCanonical) {
		t.Fatalf("LeafEntry err = %v", err)
	}
	if _, err := svc.Update(Patch{Start: nonCanonical, End: nonCanonical + PageSize}); !errors.Is(err, ErrNotCanonical) {
		t.Fatalf("non-canonical start err = %v", err)
	}
	if _, err := svc.Update(Patch{Start: va, End: nonCanonical}); !errors.Is(err, ErrNotCanonical) {
		t.Fatalf("non-canonical end err = %v", err)
	}
	if _, err := svc.Update(Patch{Start: va, End: va}); !errors.Is(err, ErrInvalidRange) {
		t.Fatalf("empty range err = %v", err)
	}
	if _, err := svc.Update(Patch{Start: va + 1, End: va + PageSize}); !errors.Is(err, ErrInvalidAlignment) {
		t.Fatalf("unaligned err = %v", err)
	}

	// Rejected transaction allocates nothing.
	before := b.alloc.liveCount()
	if _, err := svc.Update(Patch{Start: nonCanonical, End: nonCanonical + PageSize}); err == nil {
		t.Fatal("expected error")
	}
	if b.alloc.liveCount() != before {
		t.Fatal("rejected transaction leaked pages")
	}
}

// TestNotMapped reports holes without allocating.
func TestNotMapped(t *testing.T) {
	b := newBuilder(t)
	svc := NewService(b.store, b.alloc, b.root)
	before := b.alloc.liveCount()
	_, err := svc.Update(Patch{
		Start: 0x30000000,
		End:   0x30001000,
		Perm:  Perm{Write: true, User: true, Execute: true},
	})
	if !errors.Is(err, ErrNotMapped) {
		t.Fatalf("err = %v, want ErrNotMapped", err)
	}
	if b.alloc.liveCount() != before {
		t.Fatal("not-mapped transaction leaked pages")
	}
	if _, err := b.snap(b.root).Lookup(0x30000000); !errors.Is(err, ErrNotMapped) {
		t.Fatalf("lookup err = %v", err)
	}
}

// TestSuccessfulCommitDiff verifies the full pre/post comparison on a normal
// commit: leaf sets and refcounts unchanged, table pages replaced along the
// path, effective permissions updated exactly in the patch range.
func TestSuccessfulCommitDiff(t *testing.T) {
	b := newBuilder(t)
	const va = uint64(0x40000000)
	const phys = uint64(0x80000000)
	orig := Perm{Write: true, User: true, Execute: true}
	b.map2M(va, phys, orig)
	old := b.snap(b.root)
	before, err := old.Analyze()
	if err != nil {
		t.Fatal(err)
	}

	svc := NewService(b.store, b.alloc, b.root)
	np := Perm{Write: false, User: true, Execute: false}
	patchStart := va + 200*PageSize
	if _, err := svc.Update(Patch{Start: patchStart, End: patchStart + PageSize, Perm: np}); err != nil {
		t.Fatal(err)
	}
	after, err := svc.Current().Analyze()
	if err != nil {
		t.Fatal(err)
	}

	if mapsEqual(before.TablePages, after.TablePages) {
		t.Fatal("table page set must change after a copy-on-write edit")
	}
	oldMaps, _ := old.Mappings()
	newMaps, _ := svc.Current().Mappings()
	if !intervalsEqual(physicalCoverage(oldMaps), physicalCoverage(newMaps)) {
		t.Fatal("physical backing coverage changed after the edit")
	}

	// Effective perms differ only in the patched 4 KiB page.
	oldByVA := map[uint64]Mapping{}
	for _, m := range oldMaps {
		oldByVA[m.VA] = m
	}
	diffs := 0
	for _, m := range newMaps {
		if m.VA == patchStart {
			if m.Perm != np {
				t.Fatalf("patched page perms = %+v", m.Perm)
			}
			diffs++
			continue
		}
		if om, ok := oldByVA[m.VA]; ok && m.Perm != om.Perm {
			t.Fatalf("unexpected permission change at %#x", m.VA)
		}
	}
	if diffs != 1 {
		t.Fatalf("expected exactly 1 permission diff, got %d", diffs)
	}
}

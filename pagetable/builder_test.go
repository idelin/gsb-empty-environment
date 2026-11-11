package pagetable

import "testing"

// builder constructs initial page-table trees directly in the memStore.
type builder struct {
	t     *testing.T
	store *memStore
	alloc *countingAllocator
	root  uint64
}

func newBuilder(t *testing.T) *builder {
	t.Helper()
	store := newMemStore()
	alloc := newCountingAllocator(1 << 40)
	root, err := alloc.AllocPage()
	if err != nil {
		t.Fatalf("root alloc: %v", err)
	}
	return &builder{t: t, store: store, alloc: alloc, root: root}
}

func (b *builder) snap(root uint64) *Snapshot {
	return &Snapshot{store: b.store, root: root}
}

// ensureTable walks (creating tables as needed) to the table at stopLevel for
// va and returns its physical address.
func (b *builder) ensureTable(va uint64, stopLevel int) uint64 {
	table := b.root
	for level := 0; level < stopLevel; level++ {
		idx := entryIndex(va, level)
		entry, err := readEntry(b.store, table, idx)
		if err != nil {
			b.t.Fatalf("read entry: %v", err)
		}
		if entryPresent(entry) {
			table = entry & tableFrameMask
			continue
		}
		child, err := b.alloc.AllocPage()
		if err != nil {
			b.t.Fatalf("alloc child: %v", err)
		}
		entry = flagPresent | flagRW | flagUS | child
		if err := writeEntry(b.store, table, idx, entry); err != nil {
			b.t.Fatalf("write entry: %v", err)
		}
		table = child
	}
	return table
}

// map2M installs a 2 MiB leaf at va (2 MiB aligned) pointing at phys.
func (b *builder) map2M(va, phys uint64, p Perm) {
	pd := b.ensureTable(va, LevelPD)
	entry := flagPresent | (phys & largeFrameMask) | flagPS
	entry = setLeafPerm(entry, p)
	if err := writeEntry(b.store, pd, entryIndex(va, LevelPD), entry); err != nil {
		b.t.Fatalf("map2M write: %v", err)
	}
}

// map4K installs a single 4 KiB leaf by creating the PT first.
func (b *builder) map4K(va, phys uint64, p Perm) {
	pt := b.ensureTable(va, LevelPT)
	entry := flagPresent | (phys & leafFrameMask)
	entry = setLeafPerm(entry, p)
	if err := writeEntry(b.store, pt, entryIndex(va, LevelPT), entry); err != nil {
		b.t.Fatalf("map4K write: %v", err)
	}
}

// aliasPML4 makes the PML4 entry for va point at the same PDPT page used by
// src, creating an aliased subtree at a distinct virtual address.
func (b *builder) aliasPML4(src, va uint64) {
	srcEntry, err := readEntry(b.store, b.root, entryIndex(src, LevelPML4))
	if err != nil {
		b.t.Fatalf("alias read: %v", err)
	}
	if err := writeEntry(b.store, b.root, entryIndex(va, LevelPML4), srcEntry); err != nil {
		b.t.Fatalf("alias write: %v", err)
	}
}

func mustPerm(t *testing.T, s *Snapshot, va uint64, wantPerm Perm, wantPhys uint64, wantSize uint64) {
	t.Helper()
	m, err := s.Lookup(va)
	if err != nil {
		t.Fatalf("lookup %#x: %v", va, err)
	}
	if m.Perm != wantPerm || m.Phys != wantPhys || m.Size != wantSize {
		t.Fatalf("lookup %#x = perm %+v phys %#x size %#x, want perm %+v phys %#x size %#x",
			va, m.Perm, m.Phys, m.Size, wantPerm, wantPhys, wantSize)
	}
}

func mapsEqual(a, b map[uint64]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}

func intMapsEqual(a, b map[uint64]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

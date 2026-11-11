package pagetable

// txn is a private copy-on-write editing session over an immutable root.
//
// Every write goes to a freshly allocated page: pre-existing pages are never
// modified, so the old snapshot stays readable for concurrent walkers and
// every alias of a shared subtree is preserved until an entry on the exact
// path being edited is rewritten.
type txn struct {
	store PageStore
	alloc Allocator

	// copies maps an existing table page to its private replacement.
	copies map[uint64]uint64
	// owned is every physical page this transaction allocated and still has
	// to release on rollback (or write on commit).
	owned map[uint64]struct{}
}

func newTxn(store PageStore, alloc Allocator) *txn {
	return &txn{
		store:  store,
		alloc:  alloc,
		copies: map[uint64]uint64{},
		owned:  map[uint64]struct{}{},
	}
}

// allocPage allocates a zeroed frame through the restricted allocator.
// Every frame allocation in the package funnels through here, which is what
// makes per-allocation failure injection exhaustive.
func (t *txn) allocPage() (uint64, error) {
	addr, err := t.alloc.AllocPage()
	if err != nil {
		return 0, err
	}
	t.owned[addr] = struct{}{}
	return addr, nil
}

// cow returns a writable copy of tableAddr, reusing one if already made.
// Pages created by this transaction are already private.
func (t *txn) cow(tableAddr uint64) (uint64, error) {
	if priv, ok := t.copies[tableAddr]; ok {
		return priv, nil
	}
	if _, ok := t.owned[tableAddr]; ok {
		return tableAddr, nil
	}
	priv, err := t.allocPage()
	if err != nil {
		return 0, err
	}
	var buf [PageSize]byte
	if _, err := t.store.ReadAt(buf[:], int64(tableAddr)); err != nil {
		t.rollback()
		return 0, err
	}
	if _, err := t.store.WriteAt(buf[:], int64(priv)); err != nil {
		t.rollback()
		return 0, err
	}
	t.copies[tableAddr] = priv
	return priv, nil
}

// apply rewrites effective permissions for every 4 KiB page in the patch.
func (t *txn) apply(root uint64, p Patch) (uint64, error) {
	if err := p.validate(); err != nil {
		return 0, err
	}
	newRoot, err := t.cow(root)
	if err != nil {
		return 0, err
	}
	if err := t.editRange(newRoot, LevelPML4, p.Start, p.End, p.Perm); err != nil {
		return 0, err
	}
	return newRoot, nil
}

// editRange walks [start, end) recursively. Index bits are always taken from
// the full virtual address, so the same shift logic works at every depth.
func (t *txn) editRange(tableAddr uint64, level int, start, end uint64, perm Perm) error {
	// Force a writable private copy before touching any entry. Pages this
	// transaction allocated are already private and cow() returns them as-is.
	priv, err := t.cow(tableAddr)
	if err != nil {
		return err
	}
	tableAddr = priv
	span := entrySpan(level)
	shift := uint(12 + 9*(pageTableLevels-1-level))
	tableSpan := span * entriesPerTab
	tableBase := start &^ (tableSpan - 1)
	first := uint((start >> shift) & 0x1ff)
	last := uint(((end - 1) >> shift) & 0x1ff)
	// When the range wraps a table boundary (first > last), walk to the end
	// of this table; recursion into the neighbor table is driven by the
	// parent's per-entry intersection, so this only matters below the root.
	if level > LevelPML4 && first > last {
		last = entriesPerTab - 1
	}

	for idx := first; idx <= last; idx++ {
		entry, err := readEntry(t.store, tableAddr, idx)
		if err != nil {
			return err
		}
		if !entryPresent(entry) {
			return ErrNotMapped
		}

		entryVA := tableBase + uint64(idx)*span
		// Intersect the entry's span with the requested range.
		lo := start
		if entryVA > lo {
			lo = entryVA
		}
		hi := end
		if entrySpanEnd := entryVA + span; entrySpanEnd < hi {
			hi = entrySpanEnd
		}
		if lo >= hi {
			continue
		}

		if level == LevelPT {
			entry = setLeafPerm(entry, perm)
			if err := writeEntry(t.store, tableAddr, idx, entry); err != nil {
				return err
			}
			continue
		}

		if entry&flagPS != 0 {
			if level == LevelPDPT {
				return ErrUnsupportedPageSize
			}
			// 2 MiB leaf at the PD level.
			coversWhole := lo == entryVA && hi == entryVA+span
			if coversWhole {
				entry = setLeafPerm(entry, perm)
				if err := writeEntry(t.store, tableAddr, idx, entry); err != nil {
					return err
				}
				continue
			}
			ptAddr, err := t.splitLargePage(entry, entryVA)
			if err != nil {
				return err
			}
			parent := relaxForChild(entry, perm)
			parent &^= flagPS
			parent = (parent &^ tableFrameMask) | ptAddr
			if err := writeEntry(t.store, tableAddr, idx, parent); err != nil {
				return err
			}
			if err := t.editRange(ptAddr, LevelPT, lo, hi, perm); err != nil {
				return err
			}
			continue
		}

		// Plain table pointer: grant permissions upward, then descend.
		child := entry & tableFrameMask
		childPriv, err := t.cow(child)
		if err != nil {
			return err
		}
		parent := relaxForChild(entry, perm)
		parent = (parent &^ tableFrameMask) | childPriv
		if err := writeEntry(t.store, tableAddr, idx, parent); err != nil {
			return err
		}
		if err := t.editRange(childPriv, level+1, lo, hi, perm); err != nil {
			return err
		}
	}
	return nil
}

// splitLargePage breaks a 2 MiB PD leaf into 512 4 KiB PT entries.
// The physical frame chain and every preserved flag round-trip exactly:
// tryMerge rebuilds an identical raw large entry when the split is undone.
func (t *txn) splitLargePage(largeEntry, entryVA uint64) (uint64, error) {
	ptAddr, err := t.allocPage()
	if err != nil {
		return 0, err
	}
	baseFlags := largeEntry &^ (largeFrameMask | flagPS)
	if baseFlags&flagLargePAT != 0 {
		// PAT moves from bit 12 on a large entry to bit 7 on a PTE.
		baseFlags = (baseFlags &^ flagLargePAT) | flagPS
	}
	base := largeEntry & largeFrameMask
	for i := uint(0); i < entriesPerTab; i++ {
		pte := baseFlags | (base + uint64(i)*PageSize)
		if err := writeEntry(t.store, ptAddr, i, pte); err != nil {
			return 0, err
		}
	}
	return ptAddr, nil
}

// collapse re-merges 4 KiB PT pages that the transaction turned back into a
// fully uniform 2 MiB region (for example after the last patch is undone).
// Only transaction-private PT pages with a single referencing PD entry are
// eligible, so aliased and shared subtrees are never touched.
func (t *txn) collapse(root uint64) error {
	ptRefs := map[uint64]int{}
	return t.collapseTable(root, LevelPML4, ptRefs)
}

func (t *txn) collapseTable(tableAddr uint64, level int, ptRefs map[uint64]int) error {
	// Entries always live in the private copy once a table was COW-ed.
	if priv, ok := t.copies[tableAddr]; ok {
		tableAddr = priv
	}
	for i := uint(0); i < entriesPerTab; i++ {
		entry, err := readEntry(t.store, tableAddr, i)
		if err != nil {
			return err
		}
		if !entryPresent(entry) {
			continue
		}
		if level == LevelPT || (level < LevelPT && entry&flagPS != 0) {
			continue
		}
		child := entry & tableFrameMask
		if level == LevelPD {
			if _, owned := t.owned[child]; !owned {
				continue
			}
			ptRefs[child]++
			if ptRefs[child] != 1 {
				continue // aliased inside the transaction: keep the shared PT page
			}
			merged, ok, err := t.tryMerge(child)
			if err != nil {
				return err
			}
			if ok {
				if err := writeEntry(t.store, tableAddr, i, merged); err != nil {
					return err
				}
				continue
			}
		}
		if err := t.collapseTable(child, level+1, ptRefs); err != nil {
			return err
		}
	}
	return nil
}

// tryMerge returns a 2 MiB entry when pt holds 512 uniform, contiguous PTEs.
func (t *txn) tryMerge(pt uint64) (uint64, bool, error) {
	first, err := readEntry(t.store, pt, 0)
	if err != nil {
		return 0, false, err
	}
	if !entryPresent(first) {
		return 0, false, nil
	}
	base := first & leafFrameMask
	commonFlags := first &^ leafFrameMask
	for i := uint(1); i < entriesPerTab; i++ {
		e, err := readEntry(t.store, pt, i)
		if err != nil {
			return 0, false, err
		}
		if !entryPresent(e) || e&^leafFrameMask != commonFlags {
			return 0, false, nil
		}
		if e&leafFrameMask != base+uint64(i)*PageSize {
			return 0, false, nil
		}
	}
	merged := commonFlags | (base & largeFrameMask) | flagPS
	// Inverse of the PAT relocation done in splitLargePage: bit 7 on a PTE
	// (PT-level PAT) moves back to bit 12 on the large entry.
	if commonFlags&flagPS != 0 {
		merged &^= flagPS
		merged |= flagLargePAT
	}
	t.alloc.FreePage(pt)
	delete(t.owned, pt)
	for old, priv := range t.copies {
		if priv == pt {
			delete(t.copies, old)
		}
	}
	return merged, true, nil
}

// commit finalizes the transaction. All private pages have already been
// written as they were produced; collapse may recycle pages back to the
// allocator. The returned root is safe to publish atomically.
func (t *txn) commit(root uint64) (uint64, error) {
	if err := t.collapse(root); err != nil {
		t.rollback()
		return 0, err
	}
	// Reclaim any private page that copy-on-write produced but the final
	// tree no longer reaches (for example a PT copied on the way into a region
	// that collapse turned back into a single large page).
	reachable, err := t.reachableTablePages(root)
	if err != nil {
		t.rollback()
		return 0, err
	}
	for addr := range t.owned {
		if _, ok := reachable[addr]; ok {
			continue
		}
		t.alloc.FreePage(addr)
		delete(t.owned, addr)
	}
	// Pages stay owned from the allocator's perspective: they are now
	// reachable from the published root and must not be recycled.
	t.owned = map[uint64]struct{}{}
	t.copies = map[uint64]uint64{}
	return root, nil
}

// reachableTablePages walks the committed private tree and returns every
// table page (not leaf data frames) reachable from root.
func (t *txn) reachableTablePages(root uint64) (map[uint64]struct{}, error) {
	out := map[uint64]struct{}{}
	visited := map[uint64]bool{}
	var walk func(addr uint64, level int) error
	walk = func(addr uint64, level int) error {
		out[addr] = struct{}{}
		if visited[addr] {
			return nil
		}
		visited[addr] = true
		for i := uint(0); i < entriesPerTab; i++ {
			entry, err := readEntry(t.store, addr, i)
			if err != nil {
				return err
			}
			if !entryPresent(entry) {
				continue
			}
			if level < LevelPT && entry&flagPS == 0 {
				if err := walk(entry&tableFrameMask, level+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(root, LevelPML4); err != nil {
		return nil, err
	}
	return out, nil
}

// rollback returns every frame the transaction still owns. Old pages were
// never written, so the previous root is untouched.
func (t *txn) rollback() {
	for addr := range t.owned {
		t.alloc.FreePage(addr)
	}
	t.owned = map[uint64]struct{}{}
	t.copies = map[uint64]uint64{}
}

package permverifier

import "sort"

// SmallPageTable is an intentionally simple reference model: it keeps one byte
// cell per covered physical address and records, per cell, the views currently
// holding write and execute permissions. Enumeration is byte-by-byte rather
// than interval-based, so tests can cross-check the symbolic verifier against a
// trivially auditable implementation.
//
// The table covers only spans explicitly mapped into it; callers drive it one
// state transition at a time and compare conflicts after every transition.
type SmallPageTable struct {
	cells map[uint64]*smallCell
}

type smallCell struct {
	w map[ViewRef]bool
	x map[ViewRef]bool
}

// NewSmallPageTable creates an empty byte-addressed reference table.
func NewSmallPageTable() *SmallPageTable {
	return &SmallPageTable{cells: map[uint64]*smallCell{}}
}

// MapView records one view's permissions over an interval. Revoked views must
// be removed with UnmapView before permission is released.
func (t *SmallPageTable) MapView(phys Interval, ref ViewRef) {
	for k := uint64(0); k < phys.Len; k++ {
		addr, carry := add64(phys.Lo, k)
		if carry {
			return
		}
		c, ok := t.cells[addr]
		if !ok {
			c = &smallCell{w: map[ViewRef]bool{}, x: map[ViewRef]bool{}}
			t.cells[addr] = c
		}
		if ref.Perm.Has(PermWrite) {
			c.w[ref] = true
		}
		if ref.Perm.Has(PermExec) {
			c.x[ref] = true
		}
	}
}

// UnmapView removes a view from every cell it touched. A missing ack simply
// means the caller never invokes this, which is how retained old permissions
// are modeled: the bytes stay marked until every CPU confirms the revoke.
func (t *SmallPageTable) UnmapView(ref ViewRef) {
	for addr, c := range t.cells {
		delete(c.w, ref)
		delete(c.x, ref)
		if len(c.w) == 0 && len(c.x) == 0 {
			delete(t.cells, addr)
		}
	}
}

// Conflict returns the lowest physical interval of consecutive bytes that are
// simultaneously writable and executable, or nil when the table is clean.
func (t *SmallPageTable) Conflict() *Conflict {
	addrs := make([]uint64, 0, len(t.cells))
	for addr, c := range t.cells {
		if len(c.w) > 0 && len(c.x) > 0 {
			addrs = append(addrs, addr)
		}
	}
	if len(addrs) == 0 {
		return nil
	}
	sort.Slice(addrs, func(i, j int) bool { return addrs[i] < addrs[j] })
	start := addrs[0]
	end := start
	for _, addr := range addrs[1:] {
		if addr == end+1 {
			end = addr
			continue
		}
		break
	}
	c := &Conflict{Phys: Interval{Lo: start, Len: end - start + 1}}
	for ref := range t.cells[start].w {
		c.Writers = append(c.Writers, ref)
	}
	for ref := range t.cells[start].x {
		c.Execers = append(c.Execers, ref)
	}
	sortRefs(c.Writers)
	sortRefs(c.Execers)
	return c
}

// Size reports how many physical bytes currently carry any permission.
func (t *SmallPageTable) Size() int { return len(t.cells) }

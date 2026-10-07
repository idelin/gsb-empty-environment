package pageindex

import "fmt"

// Reachable walks the page reference graph from the given roots and returns
// the set of reachable page numbers.
func Reachable(p *Pager, roots ...PageNo) map[PageNo]struct{} {
	seen := make(map[PageNo]struct{})
	var walk func(no PageNo)
	walk = func(no PageNo) {
		if _, ok := seen[no]; ok {
			return
		}
		seen[no] = struct{}{}
		n := p.get(no)
		for _, k := range n.kids {
			walk(k)
		}
	}
	for _, r := range roots {
		walk(r)
	}
	return seen
}

// CheckInvariants verifies the reference graph and allocator state:
//
//   - every page reachable from the current root or any active snapshot is
//     allocated, no free page is reachable from any root, and no retired
//     page is reachable from the current root (a retired page may still be
//     pinned by an active snapshot, which is exactly why it is not free);
//   - the free list has no duplicates and no page is both free and allocated;
//   - no allocated page is leaked (allocated but neither reachable nor
//     awaiting reclamation);
//   - every reachable tree is structurally valid (key order, child counts,
//     occupancy bounds, uniform leaf depth).
func (db *DB) CheckInvariants() error {
	db.mu.Lock()
	roots := []PageNo{db.root}
	for _, ref := range db.snaps {
		roots = append(roots, ref.root)
	}
	t := db.t
	db.mu.Unlock()

	db.mu.Lock()
	current := db.root
	db.mu.Unlock()
	reachAll := Reachable(db.pager, roots...)
	reachCurrent := Reachable(db.pager, current)

	db.pager.mu.RLock()
	defer db.pager.mu.RUnlock()
	freeSet := make(map[PageNo]bool, len(db.pager.free))
	for _, no := range db.pager.free {
		if freeSet[no] {
			return fmt.Errorf("page %d duplicated in free list", no)
		}
		freeSet[no] = true
		if _, ok := reachAll[no]; ok {
			return fmt.Errorf("free page %d still reachable from a root", no)
		}
		if _, ok := db.pager.pages[no]; ok {
			return fmt.Errorf("free page %d still allocated", no)
		}
	}
	retiredSet := make(map[PageNo]bool, len(db.pager.retired))
	for _, r := range db.pager.retired {
		if _, ok := reachCurrent[r.no]; ok {
			return fmt.Errorf("retired page %d still reachable from the current root", r.no)
		}
		retiredSet[r.no] = true
	}
	for no := range reachAll {
		if _, ok := db.pager.pages[no]; !ok {
			return fmt.Errorf("reachable page %d is not allocated", no)
		}
	}
	for no := range db.pager.pages {
		if _, ok := reachAll[no]; !ok && !retiredSet[no] {
			return fmt.Errorf("page %d leaked: neither reachable nor retired", no)
		}
	}
	for _, root := range roots {
		if err := validateTree(db.pager, root, t); err != nil {
			return err
		}
	}
	return nil
}

// validateTree checks B-tree structure for the tree rooted at root.
func validateTree(p *Pager, root PageNo, t int) error {
	leafDepth := -1
	var rec func(no PageNo, lo, hi *uint64, depth int, isRoot bool) error
	rec = func(no PageNo, lo, hi *uint64, depth int, isRoot bool) error {
		n := p.get(no)
		if !isRoot && (len(n.keys) < t-1 || len(n.keys) > 2*t-1) {
			return fmt.Errorf("page %d: key count %d outside [%d,%d]", no, len(n.keys), t-1, 2*t-1)
		}
		if isRoot && !n.leaf && len(n.keys) == 0 {
			return fmt.Errorf("page %d: empty internal root", no)
		}
		for i := 1; i < len(n.keys); i++ {
			if n.keys[i-1] >= n.keys[i] {
				return fmt.Errorf("page %d: keys out of order", no)
			}
		}
		if lo != nil && len(n.keys) > 0 && n.keys[0] <= *lo {
			return fmt.Errorf("page %d: key %d violates lower bound %d", no, n.keys[0], *lo)
		}
		if hi != nil && len(n.keys) > 0 && n.keys[len(n.keys)-1] >= *hi {
			return fmt.Errorf("page %d: key %d violates upper bound %d", no, n.keys[len(n.keys)-1], *hi)
		}
		if n.leaf {
			if len(n.kids) != 0 {
				return fmt.Errorf("page %d: leaf with children", no)
			}
			if leafDepth == -1 {
				leafDepth = depth
			} else if leafDepth != depth {
				return fmt.Errorf("page %d: leaf at depth %d, want %d", no, depth, leafDepth)
			}
			return nil
		}
		if len(n.kids) != len(n.keys)+1 {
			return fmt.Errorf("page %d: %d children for %d keys", no, len(n.kids), len(n.keys))
		}
		for i, k := range n.kids {
			clo, chi := lo, hi
			if i > 0 {
				v := n.keys[i-1]
				clo = &v
			}
			if i < len(n.keys) {
				v := n.keys[i]
				chi = &v
			}
			if err := rec(k, clo, chi, depth+1, false); err != nil {
				return err
			}
		}
		return nil
	}
	return rec(root, nil, nil, 0, true)
}

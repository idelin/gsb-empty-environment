package bptree

import (
	"fmt"
)

// Validate independently traverses the tree rooted at root and checks the
// structural invariants: keys sorted and within separator bounds, separator
// keys equal to the minimum key of their right subtree, all leaves at the
// same depth, node occupancy within [minKeys, capacity] (root exempt),
// value sizes, page references valid and unique, and the leaf link chain of
// the root's own commit consistent with in-order leaf layout.
func (db *DB) Validate(root uint64) error {
	db.mu.Lock()
	numPages := db.p.numPages
	db.mu.Unlock()
	v := &validator{
		db:        db,
		numPages:  numPages,
		visited:   map[uint64]bool{},
		leafDepth: -1,
	}
	rootNode, err := db.p.readNode(root)
	if err != nil {
		return fmt.Errorf("bptree: validate: %w", err)
	}
	v.treeSeq = rootNode.version
	if _, _, _, err := v.walk(root, nil, nil, 0, true); err != nil {
		return err
	}
	// Leaf links written by the root's own commit must match the in-order
	// leaf sequence. Links of older shared leaves may legitimately point at
	// pages outside this version, so they are not checked.
	for i := 0; i+1 < len(v.leaves); i++ {
		if v.leaves[i].version == v.treeSeq && v.leaves[i].next != v.leaves[i+1].id {
			return fmt.Errorf("bptree: validate: leaf %d next=%d, want %d",
				v.leaves[i].id, v.leaves[i].next, v.leaves[i+1].id)
		}
	}
	last := v.leaves[len(v.leaves)-1]
	if last.version == v.treeSeq && last.next != 0 {
		return fmt.Errorf("bptree: validate: last leaf %d has next=%d, want 0", last.id, last.next)
	}
	return nil
}

type validator struct {
	db        *DB
	numPages  uint64
	treeSeq   uint64
	visited   map[uint64]bool
	leafDepth int
	leaves    []*node
}

// walk checks the subtree and returns (minKey, maxKey, hasKeys, err).
func (v *validator) walk(id uint64, lo, hi *int64, depth int, isRoot bool) (int64, int64, bool, error) {
	if id == 0 || id >= v.numPages {
		return 0, 0, false, fmt.Errorf("bptree: validate: page reference %d out of range [1,%d)", id, v.numPages)
	}
	if v.visited[id] {
		return 0, 0, false, fmt.Errorf("bptree: validate: page %d referenced twice", id)
	}
	v.visited[id] = true
	n, err := v.db.p.readNode(id)
	if err != nil {
		return 0, 0, false, fmt.Errorf("bptree: validate: %w", err)
	}
	if len(n.keys) > v.db.capacity {
		return 0, 0, false, fmt.Errorf("bptree: validate: page %d has %d keys, capacity %d", id, len(n.keys), v.db.capacity)
	}
	if !isRoot && len(n.keys) < v.db.minKeys {
		return 0, 0, false, fmt.Errorf("bptree: validate: page %d has %d keys, minimum %d", id, len(n.keys), v.db.minKeys)
	}
	for i := 1; i < len(n.keys); i++ {
		if n.keys[i-1] >= n.keys[i] {
			return 0, 0, false, fmt.Errorf("bptree: validate: page %d keys out of order at %d", id, i)
		}
	}
	if len(n.keys) > 0 {
		if lo != nil && n.keys[0] < *lo {
			return 0, 0, false, fmt.Errorf("bptree: validate: page %d key %d below lower bound %d", id, n.keys[0], *lo)
		}
		if hi != nil && n.keys[len(n.keys)-1] >= *hi {
			return 0, 0, false, fmt.Errorf("bptree: validate: page %d key %d above upper bound %d", id, n.keys[len(n.keys)-1], *hi)
		}
	}
	if n.leaf {
		if len(n.values) != len(n.keys) {
			return 0, 0, false, fmt.Errorf("bptree: validate: leaf %d key/value count mismatch", id)
		}
		for _, val := range n.values {
			if len(val) > MaxValueSize {
				return 0, 0, false, fmt.Errorf("bptree: validate: leaf %d value too large", id)
			}
		}
		if v.leafDepth < 0 {
			v.leafDepth = depth
		} else if v.leafDepth != depth {
			return 0, 0, false, fmt.Errorf("bptree: validate: leaf %d at depth %d, want %d", id, depth, v.leafDepth)
		}
		v.leaves = append(v.leaves, n)
		if len(n.keys) == 0 {
			if !isRoot {
				return 0, 0, false, fmt.Errorf("bptree: validate: empty non-root leaf %d", id)
			}
			return 0, 0, false, nil
		}
		return n.keys[0], n.keys[len(n.keys)-1], true, nil
	}
	if len(n.child) != len(n.keys)+1 {
		return 0, 0, false, fmt.Errorf("bptree: validate: internal %d has %d keys but %d children", id, len(n.keys), len(n.child))
	}
	var min, max int64
	has := false
	for i, cid := range n.child {
		clo := lo
		if i > 0 {
			clo = &n.keys[i-1]
		}
		chi := hi
		if i < len(n.keys) {
			chi = &n.keys[i]
		}
		cmin, cmax, chas, err := v.walk(cid, clo, chi, depth+1, false)
		if err != nil {
			return 0, 0, false, err
		}
		if !chas {
			return 0, 0, false, fmt.Errorf("bptree: validate: empty subtree at page %d child %d", id, i)
		}
		if i > 0 && cmin != n.keys[i-1] {
			return 0, 0, false, fmt.Errorf("bptree: validate: separator %d at page %d != min key %d of right subtree", n.keys[i-1], id, cmin)
		}
		if !has {
			min = cmin
		}
		max = cmax
		has = true
	}
	return min, max, has, nil
}

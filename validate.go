package bptree

import "fmt"

// Validate independently verifies the structural invariants of the tree
// rooted at root:
//   - keys are strictly increasing within every node
//   - separator keys equal the minimum key of the subtree to their right
//   - child key ranges stay within the bounds implied by the separators
//   - all leaves sit at the same depth
//   - node occupancy stays within [min, capacity] (root exempt from min)
//   - every referenced page is allocated, decodable, and referenced once
//
// It only reads pages through the store and shares no state with the tree's
// write path, so it can audit any root, current or historical.
func Validate(store *Store, root PageID, capacity int) error {
	if root == 0 {
		return fmt.Errorf("validate: null root")
	}
	v := &validator{
		store:     store,
		cap:       capacity,
		visited:   make(map[PageID]bool),
		leafDepth: -1,
	}
	_, _, _, err := v.walk(root, 0, 0, 0, false, false, true)
	return err
}

type validator struct {
	store     *Store
	cap       int
	visited   map[PageID]bool
	leafDepth int
}

func (v *validator) minKeys(leaf bool) int {
	if leaf {
		return (v.cap + 1) / 2
	}
	return v.cap / 2
}

// walk recursively validates the subtree at id. lo/hi are the key bounds
// implied by ancestor separators: lo inclusive, hi exclusive. It returns the
// subtree's min and max keys and whether it is empty.
func (v *validator) walk(id PageID, depth int, lo, hi int64, hasLo, hasHi bool, isRoot bool) (min, max int64, empty bool, err error) {
	if id == 0 {
		return 0, 0, false, fmt.Errorf("validate: null page reference")
	}
	if v.visited[id] {
		return 0, 0, false, fmt.Errorf("validate: page %d referenced more than once", id)
	}
	v.visited[id] = true

	buf, err := v.store.Read(id)
	if err != nil {
		return 0, 0, false, fmt.Errorf("validate: read page %d: %v", id, err)
	}
	n, err := decodeNode(buf)
	if err != nil {
		return 0, 0, false, fmt.Errorf("validate: decode page %d: %v", id, err)
	}
	if n.id != id {
		return 0, 0, false, fmt.Errorf("validate: page %d header id mismatch (%d)", id, n.id)
	}

	count := len(n.keys)
	if count > v.cap {
		return 0, 0, false, fmt.Errorf("validate: page %d holds %d keys, capacity %d", id, count, v.cap)
	}
	if !isRoot && count < v.minKeys(n.leaf) {
		return 0, 0, false, fmt.Errorf("validate: page %d underfull: %d keys", id, count)
	}
	for i := 1; i < count; i++ {
		if n.keys[i-1] >= n.keys[i] {
			return 0, 0, false, fmt.Errorf("validate: page %d keys not strictly increasing", id)
		}
	}

	if n.leaf {
		if count == 0 {
			if !isRoot {
				return 0, 0, false, fmt.Errorf("validate: empty non-root leaf %d", id)
			}
			return 0, 0, true, v.setLeafDepth(id, depth)
		}
		if hasLo && n.keys[0] < lo {
			return 0, 0, false, fmt.Errorf("validate: leaf %d key %d below lower bound %d", id, n.keys[0], lo)
		}
		if hasHi && n.keys[count-1] >= hi {
			return 0, 0, false, fmt.Errorf("validate: leaf %d key %d above upper bound %d", id, n.keys[count-1], hi)
		}
		if err := v.setLeafDepth(id, depth); err != nil {
			return 0, 0, false, err
		}
		return n.keys[0], n.keys[count-1], false, nil
	}

	if isRoot && count == 0 {
		return 0, 0, false, fmt.Errorf("validate: internal root %d has no keys (root not shrunk)", id)
	}
	if len(n.children) != count+1 {
		return 0, 0, false, fmt.Errorf("validate: page %d has %d keys but %d children", id, count, len(n.children))
	}

	var subMin, subMax int64
	for i, c := range n.children {
		clo, chi := lo, hi
		chasLo, chasHi := hasLo, hasHi
		if i > 0 {
			clo, chasLo = n.keys[i-1], true
		}
		if i < count {
			chi, chasHi = n.keys[i], true
		}
		cmin, cmax, cempty, err := v.walk(c, depth+1, clo, chi, chasLo, chasHi, false)
		if err != nil {
			return 0, 0, false, err
		}
		if cempty {
			return 0, 0, false, fmt.Errorf("validate: empty child subtree at page %d", c)
		}
		if i > 0 && cmin != n.keys[i-1] {
			return 0, 0, false, fmt.Errorf("validate: separator %d at page %d does not match right child min %d", n.keys[i-1], id, cmin)
		}
		if i == 0 {
			subMin = cmin
		}
		subMax = cmax
	}
	return subMin, subMax, false, nil
}

func (v *validator) setLeafDepth(id PageID, depth int) error {
	if v.leafDepth < 0 {
		v.leafDepth = depth
		return nil
	}
	if v.leafDepth != depth {
		return fmt.Errorf("validate: leaf %d at depth %d, want %d", id, depth, v.leafDepth)
	}
	return nil
}

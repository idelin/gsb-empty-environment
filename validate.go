package rtree

import "fmt"

// Validate performs an independent structural audit of the tree and returns
// the first violation found, or nil. It checks:
//   - parent/child reference consistency in both directions
//   - level numbers (all leaves at level 0, each level exactly one less
//     than its parent)
//   - node capacity: minFill..capacity for non-root nodes, 2..capacity for
//     an internal root, 0..capacity for a leaf root
//   - every internal entry rectangle is exactly the tight MBR of its child
//   - object IDs are unique, match the index map, and total Size()
//   - leaf rectangles are normalized
func (t *Tree) Validate() error {
	if t.root == nil {
		return fmt.Errorf("rtree: nil root")
	}
	if t.root.parent != nil {
		return fmt.Errorf("rtree: root has a parent")
	}
	seen := make(map[int64]bool)
	count := 0
	if err := t.validateNode(t.root, nil, t.root.level, seen, &count); err != nil {
		return err
	}
	if count != t.size {
		return fmt.Errorf("rtree: size mismatch: tree holds %d objects, size=%d", count, t.size)
	}
	if len(seen) != len(t.index) {
		return fmt.Errorf("rtree: index map holds %d ids, tree holds %d", len(t.index), len(seen))
	}
	return nil
}

func (t *Tree) validateNode(n, parent *node, level int, seen map[int64]bool, count *int) error {
	if n.parent != parent {
		return fmt.Errorf("rtree: level %d node has wrong parent pointer", n.level)
	}
	if n.level != level {
		return fmt.Errorf("rtree: node level %d, expected %d (leaves not at uniform depth)", n.level, level)
	}
	if n == t.root {
		if !n.leaf() && len(n.entries) < 2 {
			return fmt.Errorf("rtree: internal root has %d children (< 2)", len(n.entries))
		}
	} else if len(n.entries) < t.minFill || len(n.entries) > t.capacity {
		return fmt.Errorf("rtree: level %d node has %d entries, want %d..%d",
			n.level, len(n.entries), t.minFill, t.capacity)
	}
	if len(n.entries) > t.capacity {
		return fmt.Errorf("rtree: node overflows capacity: %d > %d", len(n.entries), t.capacity)
	}
	for _, e := range n.entries {
		if !e.rect.normalized() {
			return fmt.Errorf("rtree: non-normalized rect %+v", e.rect)
		}
		if n.leaf() {
			if e.child != nil {
				return fmt.Errorf("rtree: leaf entry id=%d carries a child pointer", e.id)
			}
			if seen[e.id] {
				return fmt.Errorf("rtree: duplicate object id=%d", e.id)
			}
			seen[e.id] = true
			owner, ok := t.index[e.id]
			if !ok {
				return fmt.Errorf("rtree: id=%d missing from index map", e.id)
			}
			if owner != n {
				return fmt.Errorf("rtree: index map for id=%d points at wrong leaf", e.id)
			}
			*count++
		} else {
			if e.child == nil {
				return fmt.Errorf("rtree: internal entry missing child pointer")
			}
			if e.child.parent != n {
				return fmt.Errorf("rtree: child of level %d node does not point back", n.level)
			}
			if len(e.child.entries) == 0 {
				return fmt.Errorf("rtree: internal entry references an empty child")
			}
			if want := mbrOf(e.child.entries); e.rect != want {
				return fmt.Errorf("rtree: non-tight MBR at level %d: have %+v, want %+v",
					n.level, e.rect, want)
			}
			if err := t.validateNode(e.child, n, level-1, seen, count); err != nil {
				return err
			}
		}
	}
	return nil
}

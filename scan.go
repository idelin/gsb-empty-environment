package btree

import (
	"bytes"
	"fmt"
)

// Snapshot is a read-only, stable view of one committed generation. Because
// the file is append-only and the snapshot pins its own copy of the node
// map, a snapshot stays fully defined forever — even across later commits
// that split the root. Snapshots taken before an injected crash remain
// usable afterwards.
type Snapshot struct {
	d      *DB
	gen    uint64
	rootID uint32
	m      []uint64
}

// Snapshot pins the current committed generation.
func (d *DB) Snapshot() (*Snapshot, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.crashed {
		return nil, ErrCrashed
	}
	return &Snapshot{
		d:      d,
		gen:    d.gen,
		rootID: d.rootID,
		m:      append([]uint64(nil), d.m...),
	}, nil
}

// Gen returns the generation this snapshot pins.
func (s *Snapshot) Gen() uint64 { return s.gen }

func (s *Snapshot) readNode(id uint32) (*node, error) {
	return s.d.readNode(id, s.m)
}

// Get returns the value for key, or found=false.
func (s *Snapshot) Get(key []byte) (val []byte, found bool, err error) {
	id := s.rootID
	for {
		n, err := s.readNode(id)
		if err != nil {
			return nil, false, err
		}
		if n.isLeaf() {
			i := lowerBound(n.keys, key)
			if i < len(n.keys) && bytes.Equal(n.keys[i], key) {
				return append([]byte(nil), n.vals[i]...), true, nil
			}
			return nil, false, nil
		}
		id = n.children[n.childIndex(key)]
	}
}

// Get is a point lookup on the latest committed generation.
func (d *DB) Get(key []byte) ([]byte, bool, error) {
	snap, err := d.Snapshot()
	if err != nil {
		return nil, false, err
	}
	return snap.Get(key)
}

// Iterator walks a key range in order, crossing leaf boundaries via the
// leaf chain. start is inclusive (nil = first key), end is exclusive
// (nil = unbounded).
type Iterator struct {
	s    *Snapshot
	leaf *node
	idx  int
	end  []byte
	err  error
	key  []byte
	val  []byte
}

// Iterator returns an iterator positioned before the first key >= start.
func (s *Snapshot) Iterator(start, end []byte) *Iterator {
	it := &Iterator{s: s, end: end}
	id := s.rootID
	for {
		n, err := s.readNode(id)
		if err != nil {
			it.err = err
			return it
		}
		if n.isLeaf() {
			it.leaf = n
			break
		}
		id = n.children[n.childIndex(start)]
	}
	it.idx = lowerBound(it.leaf.keys, start)
	return it
}

// Next advances to the next entry in range.
func (it *Iterator) Next() bool {
	if it.err != nil {
		return false
	}
	for it.leaf != nil {
		if it.idx < len(it.leaf.keys) {
			k := it.leaf.keys[it.idx]
			if it.end != nil && bytes.Compare(k, it.end) >= 0 {
				it.leaf = nil
				return false
			}
			it.key, it.val = k, it.leaf.vals[it.idx]
			it.idx++
			return true
		}
		next := it.leaf.next
		if next == nilID {
			it.leaf = nil
			return false
		}
		n, err := it.s.readNode(next)
		if err != nil {
			it.err = err
			return false
		}
		it.leaf = n
		it.idx = 0
	}
	return false
}

// Key returns the current key (valid until the next Next call).
func (it *Iterator) Key() []byte { return it.key }

// Value returns the current value (valid until the next Next call).
func (it *Iterator) Value() []byte { return it.val }

// Err returns the first error encountered, if any.
func (it *Iterator) Err() error { return it.err }

// Scan calls fn for every key in [start, end) in order, stopping early if
// fn returns false.
func (s *Snapshot) Scan(start, end []byte, fn func(k, v []byte) bool) error {
	it := s.Iterator(start, end)
	for it.Next() {
		if !fn(it.Key(), it.Value()) {
			break
		}
	}
	return it.Err()
}

// Scan ranges over the latest committed generation.
func (d *DB) Scan(start, end []byte, fn func(k, v []byte) bool) error {
	snap, err := d.Snapshot()
	if err != nil {
		return err
	}
	return snap.Scan(start, end, fn)
}

// Height returns the tree height (root-only tree = 1).
func (s *Snapshot) Height() (int, error) {
	h := 0
	id := s.rootID
	for {
		n, err := s.readNode(id)
		if err != nil {
			return 0, err
		}
		h++
		if n.isLeaf() {
			return h, nil
		}
		id = n.children[0]
	}
}

// Check verifies the structural invariants of the snapshot:
//   - keys strictly sorted inside every node
//   - internal separator keys[i] == minimum key of children[i+1]
//   - all leaves at the same depth
//   - the leaf chain (next pointers) visits exactly the in-order leaves
//   - keys strictly increase along the whole leaf chain
func (s *Snapshot) Check() error {
	_, leaves, _, err := s.checkNode(s.rootID, 0)
	if err != nil {
		return err
	}
	// Walk the leaf chain from the leftmost leaf.
	var chain []uint32
	seen := make(map[uint32]bool)
	for cur := leaves[0]; cur != nilID; {
		if seen[cur] {
			return fmt.Errorf("btree: leaf chain cycle at node %d", cur)
		}
		seen[cur] = true
		chain = append(chain, cur)
		n, err := s.readNode(cur)
		if err != nil {
			return err
		}
		if !n.isLeaf() {
			return fmt.Errorf("btree: leaf chain reaches non-leaf node %d", cur)
		}
		cur = n.next
	}
	if len(chain) != len(leaves) {
		return fmt.Errorf("btree: leaf chain has %d leaves, in-order traversal has %d", len(chain), len(leaves))
	}
	for i := range chain {
		if chain[i] != leaves[i] {
			return fmt.Errorf("btree: leaf chain order mismatch at position %d (chain=%d, in-order=%d)", i, chain[i], leaves[i])
		}
	}
	// Keys strictly increase across the whole chain (no duplicates, no gaps
	// in coverage: every leaf is reachable and ordered).
	var prev []byte
	for _, id := range chain {
		n, err := s.readNode(id)
		if err != nil {
			return err
		}
		for _, k := range n.keys {
			if prev != nil && bytes.Compare(prev, k) >= 0 {
				return fmt.Errorf("btree: leaf chain keys out of order: %x then %x", prev, k)
			}
			prev = k
		}
	}
	return nil
}

// Check verifies the latest committed generation.
func (d *DB) Check() error {
	snap, err := d.Snapshot()
	if err != nil {
		return err
	}
	return snap.Check()
}

// checkNode recursively validates one subtree, returning its minimum key,
// its in-order leaf IDs, and the depth of its leaves.
func (s *Snapshot) checkNode(id uint32, depth int) (min []byte, leafIDs []uint32, leafDepth int, err error) {
	n, err := s.readNode(id)
	if err != nil {
		return nil, nil, 0, err
	}
	for i := 1; i < len(n.keys); i++ {
		if bytes.Compare(n.keys[i-1], n.keys[i]) >= 0 {
			return nil, nil, 0, fmt.Errorf("btree: node %d keys not strictly sorted", id)
		}
	}
	if n.isLeaf() {
		if len(n.keys) > 0 {
			min = n.keys[0]
		}
		return min, []uint32{id}, depth, nil
	}
	if len(n.children) != len(n.keys)+1 {
		return nil, nil, 0, fmt.Errorf("btree: node %d has %d keys but %d children", id, len(n.keys), len(n.children))
	}
	leafDepth = -1
	for i, cid := range n.children {
		cmin, cleaves, cdepth, cerr := s.checkNode(cid, depth+1)
		if cerr != nil {
			return nil, nil, 0, cerr
		}
		if i == 0 {
			min = cmin
		} else if !bytes.Equal(n.keys[i-1], cmin) {
			return nil, nil, 0, fmt.Errorf("btree: node %d separator %d = %x, but child %d min = %x",
				id, i-1, n.keys[i-1], cid, cmin)
		}
		if leafDepth == -1 {
			leafDepth = cdepth
		} else if leafDepth != cdepth {
			return nil, nil, 0, fmt.Errorf("btree: unbalanced tree under node %d", id)
		}
		leafIDs = append(leafIDs, cleaves...)
	}
	return min, leafIDs, leafDepth, nil
}

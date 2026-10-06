package integrity

import "sort"

// Proof is a membership proof for a single block. It carries the block
// index and the total block count so the verifier can bind the leaf to
// its exact position in this snapshot.
type Proof struct {
	Index     uint64
	Total     uint64
	BlockHash []byte // nil iff Missing
	Missing   bool
	Siblings  [][]byte // sibling hashes, leaf level first
}

// BatchProof proves several blocks of the same snapshot at once. Nodes
// holds the frontier sibling hashes shared by all proven paths, keyed by
// nodeID. Blocks[i] corresponds to Indices[i]; a nil entry means the
// block is missing (explicit empty leaf), which is distinct from a
// present block whose bytes are all zero.
type BatchProof struct {
	Total   uint64
	Indices []uint64 // sorted ascending, unique
	Blocks  [][]byte // parallel to Indices; nil = missing block
	Nodes   map[uint64][]byte
}

// UpdateProof proves that a snapshot with root OldRoot was turned into a
// snapshot with root NewRoot by changing only the block at Index, with
// the block count unchanged.
type UpdateProof struct {
	Index       uint64
	Total       uint64
	OldRoot     []byte
	NewRoot     []byte
	OldBlock    []byte // nil = block was missing
	NewBlock    []byte // nil = block is now missing
	OldSiblings [][]byte
	NewSiblings [][]byte
}

// nodeID identifies a node in the padded perfect tree: level 0 is the
// leaf level, pos is the index within the level. The level is packed
// into the high bits so IDs cannot collide across levels.
func nodeID(level, pos uint64) uint64 {
	return (level << 48) | pos
}

// Prove returns a membership proof for block index together with the
// root of the snapshot the proof was generated from.
func (t *Tree) Prove(index uint64) (Proof, []byte, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if index >= t.total {
		return Proof{}, nil, ErrIndexOutOfRange
	}
	p := Proof{
		Index:    index,
		Total:    t.total,
		Siblings: t.siblingsAt(index),
	}
	if t.blocks[index] == nil {
		p.Missing = true
	} else {
		p.BlockHash = clone(t.blocks[index])
	}
	return p, clone(t.root()), nil
}

// ProveBatch returns one proof covering all given indices of the same
// snapshot, together with the snapshot root. Duplicate and out-of-range
// indices are rejected; the returned indices are sorted ascending.
func (t *Tree) ProveBatch(indices []uint64) (BatchProof, []byte, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if len(indices) == 0 {
		return BatchProof{}, nil, ErrEmptyProof
	}
	sorted := make([]uint64, len(indices))
	copy(sorted, indices)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	for i, idx := range sorted {
		if idx >= t.total {
			return BatchProof{}, nil, ErrIndexOutOfRange
		}
		if i > 0 && idx == sorted[i-1] {
			return BatchProof{}, nil, ErrIndexOutOfRange
		}
	}

	bp := BatchProof{
		Total:   t.total,
		Indices: sorted,
		Blocks:  make([][]byte, len(sorted)),
		Nodes:   make(map[uint64][]byte),
	}
	for i, idx := range sorted {
		bp.Blocks[i] = clone(t.blocks[idx])
	}

	// Collect the frontier: siblings of covered nodes that are not
	// themselves covered, level by level.
	depth := len(t.layers) - 1
	covered := make(map[uint64]bool, len(sorted))
	for _, idx := range sorted {
		covered[idx] = true
	}
	for level := 0; level < depth; level++ {
		next := make(map[uint64]bool, len(covered))
		for pos := range covered {
			sib := pos ^ 1
			if !covered[sib] {
				bp.Nodes[nodeID(uint64(level), sib)] = clone(t.layers[level][sib])
			}
			next[pos>>1] = true
		}
		covered = next
	}
	return bp, clone(t.root()), nil
}

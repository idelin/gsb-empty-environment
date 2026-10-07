package integrity

import "errors"

// ErrOutOfRange is returned when a block index is not below the total
// block count of the tree.
var ErrOutOfRange = errors.New("integrity: block index out of range")

// Tree is a binary digest tree over a fixed number of blocks. Blocks that
// were never written are explicit empty leaves; the leaf level is padded
// with empty leaves up to a power of two.
//
// levels[0] holds the padded leaves and levels[len(levels)-1] holds the
// single root. A nil entry in blocks means the block is missing.
type Tree struct {
	total  uint64
	size   uint64 // padded leaf count, always a power of two
	blocks [][]byte
	levels [][]Hash
}

// NewTree builds a digest tree for total blocks, using blocks[i] as the
// content of block i (nil means the block is missing). blocks may be
// shorter than total; trailing blocks are treated as missing.
func NewTree(total uint64, blocks [][]byte) *Tree {
	size := paddedSize(total)
	t := &Tree{
		total:  total,
		size:   size,
		blocks: make([][]byte, total),
		levels: make([][]Hash, depthOf(total)+1),
	}
	copy(t.blocks, blocks)
	leaves := make([]Hash, size)
	for i := uint64(0); i < size; i++ {
		if i < total && t.blocks[i] != nil {
			leaves[i] = leafHashData(i, total, t.blocks[i])
		} else {
			leaves[i] = leafHashEmpty(i, total)
		}
	}
	t.levels[0] = leaves
	for d := 1; d < len(t.levels); d++ {
		prev := t.levels[d-1]
		level := make([]Hash, len(prev)/2)
		for i := range level {
			level[i] = interiorHash(prev[2*i], prev[2*i+1])
		}
		t.levels[d] = level
	}
	return t
}

// Total returns the total block count the tree commits to.
func (t *Tree) Total() uint64 { return t.total }

// Root returns the root hash of the tree.
func (t *Tree) Root() Hash { return t.levels[len(t.levels)-1][0] }

// Block returns the content of block i, or nil if the block is missing.
func (t *Tree) Block(i uint64) ([]byte, error) {
	if i >= t.total {
		return nil, ErrOutOfRange
	}
	return t.blocks[i], nil
}

// Update performs a local update: it replaces the content of block index
// (nil marks the block missing again) and recomputes only the hashes on
// the path from that leaf to the root. The total block count is unchanged.
func (t *Tree) Update(index uint64, data []byte) error {
	if index >= t.total {
		return ErrOutOfRange
	}
	t.blocks[index] = data
	if data != nil {
		t.levels[0][index] = leafHashData(index, t.total, data)
	} else {
		t.levels[0][index] = leafHashEmpty(index, t.total)
	}
	pos := index
	for d := 1; d < len(t.levels); d++ {
		parent := pos / 2
		t.levels[d][parent] = interiorHash(t.levels[d-1][2*parent], t.levels[d-1][2*parent+1])
		pos = parent
	}
	return nil
}

// siblings returns the sibling hashes on the path from leaf index to the
// root, ordered from the leaf level upwards.
func (t *Tree) siblings(index uint64) []Hash {
	depth := len(t.levels) - 1
	sibs := make([]Hash, depth)
	pos := index
	for d := 0; d < depth; d++ {
		if pos%2 == 0 {
			sibs[d] = t.levels[d][pos+1]
		} else {
			sibs[d] = t.levels[d][pos-1]
		}
		pos /= 2
	}
	return sibs
}

// Prove creates an inclusion proof for block index. The proof records
// whether the block is present and its content if so; missing blocks are
// proven via their explicit empty leaf.
func (t *Tree) Prove(index uint64) (*Proof, error) {
	if index >= t.total {
		return nil, ErrOutOfRange
	}
	data := t.blocks[index]
	return &Proof{
		Index:    index,
		Total:    t.total,
		Present:  data != nil,
		Data:     data,
		Siblings: t.siblings(index),
	}, nil
}

// ProveBatch creates a multi-block proof for the given indices, all within
// the current snapshot. Sibling nodes shared by several paths are
// de-duplicated; each node is emitted together with its level and position
// so the verifier can check the consistency of shared path nodes.
func (t *Tree) ProveBatch(indices []uint64) (*BatchProof, error) {
	bp := &BatchProof{Total: t.total}
	seen := make(map[nodeID]bool)
	for _, index := range indices {
		if index >= t.total {
			return nil, ErrOutOfRange
		}
		data := t.blocks[index]
		bp.Indices = append(bp.Indices, index)
		bp.Present = append(bp.Present, data != nil)
		bp.Datas = append(bp.Datas, data)
		pos := index
		for d := 0; d < len(t.levels)-1; d++ {
			var sib uint64
			if pos%2 == 0 {
				sib = pos + 1
			} else {
				sib = pos - 1
			}
			id := nodeID{level: d, index: sib}
			if !seen[id] {
				seen[id] = true
				bp.Nodes = append(bp.Nodes, ProofNode{Level: d, Index: sib, Hash: t.levels[d][sib]})
			}
			pos /= 2
		}
	}
	return bp, nil
}

// ProveUpdate creates a local-update proof for replacing the content of
// block index with newData. The proof binds the pre-update value, the
// post-update value, the shared sibling path and the block count, so a
// verifier can check the transition from the old root to the new root
// without holding the tree.
func (t *Tree) ProveUpdate(index uint64, newData []byte) (*UpdateProof, error) {
	if index >= t.total {
		return nil, ErrOutOfRange
	}
	old := t.blocks[index]
	return &UpdateProof{
		Index:      index,
		Total:      t.total,
		OldPresent: old != nil,
		OldData:    old,
		NewPresent: newData != nil,
		NewData:    newData,
		Siblings:   t.siblings(index),
	}, nil
}

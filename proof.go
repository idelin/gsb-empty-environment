package integrity

// Proof is an inclusion proof for a single block. It commits to the block
// index and the total block count, so a leaf from another position or from
// a tree with a different block count cannot be passed off as this block.
type Proof struct {
	Index    uint64
	Total    uint64
	Present  bool   // false proves the block is missing (explicit empty leaf)
	Data     []byte // block content, only meaningful when Present is true
	Siblings []Hash // sibling hashes from the leaf level up to the root
}

// leafHash derives the leaf hash committed to by the proof. Missing blocks
// use the explicit empty leaf, which is distinct from any data leaf,
// including an all-zero block.
func (p *Proof) leafHash() Hash {
	if p.Present {
		return leafHashData(p.Index, p.Total, p.Data)
	}
	return leafHashEmpty(p.Index, p.Total)
}

// computeRoot walks the sibling path from the leaf to the root. The
// left/right position at every level is derived from the proof's own block
// index, so a path assembled for a different position does not verify.
func computeRoot(leaf Hash, index uint64, siblings []Hash) Hash {
	node := leaf
	pos := index
	for _, sib := range siblings {
		if pos%2 == 0 {
			node = interiorHash(node, sib)
		} else {
			node = interiorHash(sib, node)
		}
		pos /= 2
	}
	return node
}

// validShape reports whether the proof's index, total and path length are
// consistent with each other.
func validShape(index, total uint64, siblings int) bool {
	return index < total && siblings == depthOf(total)
}

// VerifyProof checks the inclusion proof against the expected root. It
// returns true only if the proof is well shaped and recomputes to root.
func VerifyProof(root Hash, p *Proof) bool {
	if p == nil || !validShape(p.Index, p.Total, len(p.Siblings)) {
		return false
	}
	return computeRoot(p.leafHash(), p.Index, p.Siblings) == root
}

// nodeID identifies a tree node by its level (0 = leaf level) and its
// position within that level.
type nodeID struct {
	level int
	index uint64
}

// ProofNode is a sibling node in a batch proof, tagged with its position
// so that nodes shared by several proven blocks can be cross-checked.
type ProofNode struct {
	Level int
	Index uint64
	Hash  Hash
}

// BatchProof proves several blocks of the same snapshot at once. Nodes on
// shared paths appear only once; the verifier rebuilds a position-indexed
// node map and rejects conflicting duplicates.
type BatchProof struct {
	Total   uint64
	Indices []uint64
	Present []bool
	Datas   [][]byte
	Nodes   []ProofNode
}

// VerifyBatchProof checks a multi-block proof against the expected root.
// Every proven block must recompute to the same root, and any two entries
// that claim the same tree position must carry the same hash; conflicting
// or missing shared nodes make the verification fail.
func VerifyBatchProof(root Hash, bp *BatchProof) bool {
	if bp == nil || len(bp.Indices) == 0 {
		return false
	}
	if len(bp.Present) != len(bp.Indices) || len(bp.Datas) != len(bp.Indices) {
		return false
	}
	depth := depthOf(bp.Total)
	nodes := make(map[nodeID]Hash, len(bp.Nodes))
	for _, n := range bp.Nodes {
		if n.Level < 0 || n.Level >= depth || n.Index >= paddedSize(bp.Total)>>uint(n.Level) {
			return false
		}
		id := nodeID{level: n.Level, index: n.Index}
		if prev, ok := nodes[id]; ok && prev != n.Hash {
			return false // conflicting claims for a shared path node
		}
		nodes[id] = n.Hash
	}
	for k, index := range bp.Indices {
		if index >= bp.Total {
			return false
		}
		var leaf Hash
		if bp.Present[k] {
			leaf = leafHashData(index, bp.Total, bp.Datas[k])
		} else {
			leaf = leafHashEmpty(index, bp.Total)
		}
		node := leaf
		pos := index
		for d := 0; d < depth; d++ {
			var sib uint64
			if pos%2 == 0 {
				sib = pos + 1
			} else {
				sib = pos - 1
			}
			hash, ok := nodes[nodeID{level: d, index: sib}]
			if !ok {
				return false
			}
			if pos%2 == 0 {
				node = interiorHash(node, hash)
			} else {
				node = interiorHash(hash, node)
			}
			pos /= 2
		}
		if node != root {
			return false
		}
	}
	return true
}

// UpdateProof proves a local update: the transition of one block from its
// old value to a new value. It binds the old root, the new root and the
// total block count, because both roots are recomputed from the same
// sibling path and the leaf hashes commit to the block count.
type UpdateProof struct {
	Index      uint64
	Total      uint64
	OldPresent bool
	OldData    []byte
	NewPresent bool
	NewData    []byte
	Siblings   []Hash
}

// VerifyUpdateProof checks that the update proof describes a valid
// transition from oldRoot to newRoot, changing exactly the leaf at the
// committed block index. Mixing a leaf or a path from another snapshot
// cannot verify, because the sibling path must recompute to both roots.
func VerifyUpdateProof(oldRoot, newRoot Hash, up *UpdateProof) bool {
	if up == nil || !validShape(up.Index, up.Total, len(up.Siblings)) {
		return false
	}
	var oldLeaf, newLeaf Hash
	if up.OldPresent {
		oldLeaf = leafHashData(up.Index, up.Total, up.OldData)
	} else {
		oldLeaf = leafHashEmpty(up.Index, up.Total)
	}
	if up.NewPresent {
		newLeaf = leafHashData(up.Index, up.Total, up.NewData)
	} else {
		newLeaf = leafHashEmpty(up.Index, up.Total)
	}
	if computeRoot(oldLeaf, up.Index, up.Siblings) != oldRoot {
		return false
	}
	return computeRoot(newLeaf, up.Index, up.Siblings) == newRoot
}

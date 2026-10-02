package blockindex

import "sort"

// PathNode is one summary-tree level of a proof: the complete child list of
// the node on the path, plus which child continues the path. Carrying the
// full child list lets the verifier recompute the node digest and read the
// committed ranges/counts of the siblings adjacent to the path.
type PathNode struct {
	Version    uint64
	Level      uint64
	Children   []ChildInfo
	ChildIndex int
}

// BlockProof proves statements about one leaf block: membership of a key,
// non-membership in a committed gap, the rank of a key, or the entry at a
// rank (select). The same object serves all four; the verifier recomputes
// every derived fact.
type BlockProof struct {
	Version    uint64
	BlockIndex uint64 // leaf position of the block among all blocks
	Block      []byte // serialized block (the only data block the verifier needs)
	Path       []PathNode
}

// RangeProof is a query certificate for [lo, hi]. Items holds the proofs of
// all blocks intersecting the range, in consecutive block order. When no
// block intersects, Boundary holds a proof of a single block whose committed
// neighbourhood excludes the whole range.
type RangeProof struct {
	Version  uint64
	Items    []BlockProof
	Boundary *BlockProof
}

// blockForKey returns the last block whose MinKey <= key, or block 0.
func (idx *Index) blockForKey(key uint64) int {
	n := len(idx.blockInfo)
	i := sort.Search(n, func(i int) bool { return idx.blockInfo[i].MinKey > key })
	if i == 0 {
		return 0
	}
	return i - 1
}

// proveBlock assembles the proof for block b.
func (idx *Index) proveBlock(b int) *BlockProof {
	p := &BlockProof{
		Version:    idx.Version,
		BlockIndex: uint64(b),
		Block:      idx.Blocks[b],
	}
	child := b
	for lvl := 0; lvl < len(idx.Levels); lvl++ {
		nodeIdx := child / idx.Fanout
		_, _, children, err := DecodeNode(idx.Levels[lvl][nodeIdx])
		if err != nil {
			panic("blockindex: corrupt summary tree: " + err.Error())
		}
		p.Path = append(p.Path, PathNode{
			Version:    idx.Version,
			Level:      uint64(lvl + 1),
			Children:   children,
			ChildIndex: child % idx.Fanout,
		})
		child = nodeIdx
	}
	return p
}

// Prove returns a proof for the block responsible for key. VerifyMembership
// decides from it whether key is a member (with value and rank) or provably
// absent.
func (idx *Index) Prove(key uint64) *BlockProof {
	if idx.NumBlocks() == 0 {
		return &BlockProof{Version: idx.Version}
	}
	return idx.proveBlock(idx.blockForKey(key))
}

// ProveSelect returns a proof for the entry with the given rank (number of
// keys strictly smaller). It returns nil if rank is out of range.
func (idx *Index) ProveSelect(rank uint64) *BlockProof {
	if rank >= idx.Root.TotalEntries {
		return nil
	}
	var base uint64
	for b, info := range idx.blockInfo {
		if rank < base+info.Entries {
			return idx.proveBlock(b)
		}
		base += info.Entries
	}
	return nil
}

// ProveRange returns a query certificate for all entries with lo <= key <= hi.
func (idx *Index) ProveRange(lo, hi uint64) *RangeProof {
	rp := &RangeProof{Version: idx.Version}
	n := len(idx.blockInfo)
	if n == 0 || lo > hi {
		return rp
	}
	// First block that could contain keys >= lo.
	first := sort.Search(n, func(i int) bool { return idx.blockInfo[i].MaxKey >= lo })
	if first == n {
		// Every block lies entirely below lo.
		rp.Boundary = idx.proveBlock(n - 1)
		return rp
	}
	if idx.blockInfo[first].MinKey > hi {
		// No block intersects [lo, hi]; block first lies entirely above it.
		rp.Boundary = idx.proveBlock(first)
		return rp
	}
	last := sort.Search(n, func(i int) bool { return idx.blockInfo[i].MinKey > hi }) - 1
	for b := first; b <= last; b++ {
		rp.Items = append(rp.Items, *idx.proveBlock(b))
	}
	return rp
}

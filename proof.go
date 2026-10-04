package blkidx

// PathLevel is one internal summary node on the path from a leaf to the
// root. ChildIdx identifies the child the path descends into; Children is
// the full ordered child list, so the verifier can recompute the node hash
// and re-derive aggregate counts.
type PathLevel struct {
	NodeID   uint32
	Version  uint32
	ChildIdx int
	Children []ChildDesc
}

// MembershipProof proves that a key is present at a committed position with
// a committed value, or that it is absent (its would-be neighbours straddle
// it). Rank is the number of entries with smaller keys; for a present key it
// equals the entry's global position.
type MembershipProof struct {
	Key   string
	Found bool
	Rank  uint64
	Value []byte
	Leaf  []byte      // the single involved leaf block, fully encoded
	Path  []PathLevel // bottom-up, empty when the root is the leaf itself
}

// Size estimates the serialized proof size in bytes.
func (p *MembershipProof) Size() int {
	n := len(p.Leaf) + len(p.Key) + len(p.Value) + 16
	for _, lvl := range p.Path {
		n += 16 + len(encodeInternal(lvl.Version, lvl.NodeID, lvl.Children))
	}
	return n
}

// RangeProof is a query certificate for [Lo, Hi]. It carries every leaf
// block overlapping the range plus the boundary blocks holding the
// predecessor of Lo and the successor of Hi, each with its path to the
// root. Offset chaining plus the committed order let the verifier exclude
// omitted blocks; it does not merely prove the returned items exist.
type RangeProof struct {
	Lo, Hi string
	Blocks [][]byte      // consecutive encoded leaf blocks
	Paths  [][]PathLevel // per block, bottom-up
}

// Size estimates the serialized proof size in bytes, deduplicating internal
// levels shared by adjacent block paths.
func (p *RangeProof) Size() int {
	n := len(p.Lo) + len(p.Hi) + 16
	for _, b := range p.Blocks {
		n += len(b)
	}
	seen := make(map[uint32]bool)
	for _, path := range p.Paths {
		for _, lvl := range path {
			if !seen[lvl.NodeID] {
				seen[lvl.NodeID] = true
				n += 16 + len(encodeInternal(lvl.Version, lvl.NodeID, lvl.Children))
			}
		}
	}
	return n
}

// ProveMembership generates a membership/absence proof for key.
func (ix *Index) ProveMembership(key string) *MembershipProof {
	leafIdx, idx, found := ix.locate(key)
	p := &MembershipProof{
		Key:  key,
		Leaf: ix.leaves[leafIdx],
		Path: ix.pathForLeaf(leafIdx),
		Rank: ix.leafDesc[leafIdx].BaseOff + uint64(idx),
	}
	if found {
		_, entries, err := decodeLeaf(ix.leaves[leafIdx])
		if err != nil {
			panic(err)
		}
		p.Found = true
		p.Value = entries[idx].Value
	}
	return p
}

// ProveSelect generates a proof for the entry at global position j
// (0-based): the membership proof binds both the position and the value.
func (ix *Index) ProveSelect(j uint64) *MembershipProof {
	if j >= ix.total {
		return nil
	}
	leafIdx := ix.leafContaining(j)
	_, entries, err := decodeLeaf(ix.leaves[leafIdx])
	if err != nil {
		panic(err)
	}
	e := entries[j-ix.leafDesc[leafIdx].BaseOff]
	p := ix.ProveMembership(e.Key)
	if p.Rank != j {
		panic("rank/select inconsistency")
	}
	return p
}

// ProveRange generates a completeness certificate for the query [lo, hi].
func (ix *Index) ProveRange(lo, hi string) *RangeProof {
	startOff := ix.rank(lo) // number of entries < lo
	endOff := startOff
	// number of entries <= hi
	{
		leafIdx, idx, found := ix.locate(hi)
		endOff = ix.leafDesc[leafIdx].BaseOff + uint64(idx)
		if found {
			endOff++
		}
	}

	var b0, b1 int
	if startOff == 0 {
		b0 = 0
	} else {
		// include the block holding the predecessor entry startOff-1
		b0 = ix.leafContaining(startOff - 1)
	}
	if endOff >= ix.total {
		b1 = len(ix.leaves) - 1
	} else {
		// include the block holding the successor entry endOff
		b1 = ix.leafContaining(endOff)
	}

	p := &RangeProof{Lo: lo, Hi: hi}
	for b := b0; b <= b1; b++ {
		p.Blocks = append(p.Blocks, ix.leaves[b])
		p.Paths = append(p.Paths, ix.pathForLeaf(b))
	}
	return p
}

// AccessBound returns the maximum number of leaf blocks and internal nodes
// a membership proof for this index touches: 1 leaf + Depth internal nodes.
func (ix *Index) AccessBound() (leaves, nodes int) {
	return 1, ix.depth
}

// MembershipProofBound returns a byte upper bound for membership proofs,
// given the maximum encoded block size and maximum key length.
func (ix *Index) MembershipProofBound(maxBlockBytes, maxKeyLen int) int {
	perLevel := 16 + 4 + 4 + 4 + 2 + ix.cfg.Fanout*(32+8+4+2+maxKeyLen)
	return maxBlockBytes + 16 + ix.depth*perLevel
}

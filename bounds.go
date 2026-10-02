package blockindex

// Proof size and verification-access accounting.
//
// Wire size of a proof: the serialized block plus, per summary level, the
// full serialized node on the path (its child list carries the sibling
// digests, ranges and counts). With fanout F and B blocks the height is
// ceil(log_F(B)) (at least 1 for a non-empty index), so a point proof is
// O(F * log_F B) bytes and the verifier reads exactly one data block.

const (
	// NodeHeaderMax bounds magic + version + level + child count.
	NodeHeaderMax = 4 + 3*MaxVarintLen
	// ChildInfoMax bounds one serialized child entry.
	ChildInfoMax = 4*MaxVarintLen + DigestLen
)

// Height returns the summary-tree height for numBlocks leaf blocks.
func Height(numBlocks, fanout int) int {
	if numBlocks == 0 {
		return 0
	}
	h := 0
	for n := numBlocks; ; {
		n = (n + fanout - 1) / fanout
		h++
		if n <= 1 {
			return h
		}
	}
}

// MaxProofBytes bounds the wire size of a point proof.
func MaxProofBytes(numBlocks, fanout, maxBlockBytes int) int {
	if numBlocks == 0 {
		return 2 * MaxVarintLen
	}
	return 3*MaxVarintLen + maxBlockBytes +
		Height(numBlocks, fanout)*(NodeHeaderMax+fanout*ChildInfoMax+MaxVarintLen)
}

// Size returns the exact wire size of a block proof: block bytes plus the
// encoded path nodes plus varint overhead for version, block index and the
// per-level child index.
func (p *BlockProof) Size() int {
	if p == nil {
		return 0
	}
	n := 2 * MaxVarintLen
	n += len(p.Block)
	for _, pn := range p.Path {
		n += MaxVarintLen
		n += len(EncodeNode(pn.Version, pn.Level, pn.Children))
	}
	return n
}

// Size returns the exact wire size of a range proof.
func (rp *RangeProof) Size() int {
	if rp == nil {
		return 0
	}
	n := MaxVarintLen
	for i := range rp.Items {
		n += rp.Items[i].Size()
	}
	if rp.Boundary != nil {
		n += rp.Boundary.Size()
	}
	return n
}

// AccessStats records what a verification actually touched.
type AccessStats struct {
	BlocksRead     int // data blocks the verifier needed
	ChildInfosRead int // child summaries inspected along all paths
	ProofBytes     int // wire size of the proof
}

// AccessStatsOf computes the access profile of a verified block proof.
func AccessStatsOf(p *BlockProof) AccessStats {
	st := AccessStats{BlocksRead: 1, ProofBytes: p.Size()}
	for _, pn := range p.Path {
		st.ChildInfosRead += len(pn.Children)
	}
	return st
}

// MaxAccessStats bounds the access profile of any point verification.
func MaxAccessStats(numBlocks, fanout int) AccessStats {
	return AccessStats{
		BlocksRead:     1,
		ChildInfosRead: Height(numBlocks, fanout) * fanout,
	}
}

package blockindex

import (
	"errors"
	"fmt"
	"sort"
)

// MembershipResult is the outcome of VerifyMembership.
type MembershipResult struct {
	Member bool
	Key    uint64
	Value  []byte // present iff Member
	Rank   uint64 // number of keys strictly smaller than Key; valid iff Member
}

// blockContext collects the facts a verified block proof establishes.
type blockContext struct {
	entries     []Entry
	leftEntries uint64 // entries committed in blocks left of this block
	predMax     uint64 // max key of the immediately preceding block
	hasPred     bool
	succMin     uint64 // min key of the immediately succeeding block
	hasSucc     bool
}

// verifyBlockPath checks a block proof against the root summary and returns
// the established context. It never touches the index; the proof bytes are
// the only data read.
func verifyBlockPath(root RootSummary, bp *BlockProof) (*blockContext, error) {
	if bp == nil {
		return nil, errors.New("blockindex: nil proof")
	}
	if bp.Version != root.Version {
		return nil, fmt.Errorf("blockindex: proof version %d != root version %d", bp.Version, root.Version)
	}
	if len(bp.Path) != root.Height {
		return nil, fmt.Errorf("blockindex: path length %d != root height %d", len(bp.Path), root.Height)
	}
	bversion, entries, err := DecodeBlock(bp.Block)
	if err != nil {
		return nil, fmt.Errorf("blockindex: bad block: %w", err)
	}
	if bversion != root.Version {
		return nil, fmt.Errorf("blockindex: block version %d != root version %d", bversion, root.Version)
	}
	if len(entries) == 0 {
		return nil, errors.New("blockindex: empty block in proof")
	}

	// Fold the committed child info upward, starting from the leaf block.
	cur := ChildInfo{
		MinKey:  entries[0].Key,
		MaxKey:  entries[len(entries)-1].Key,
		Entries: uint64(len(entries)),
		Blocks:  1,
		Digest:  BlockDigest(bp.Block),
	}
	ctx := &blockContext{entries: entries}
	for i, pn := range bp.Path {
		if pn.Version != root.Version {
			return nil, fmt.Errorf("blockindex: path node %d version %d != root version %d", i, pn.Version, root.Version)
		}
		if pn.Level != uint64(i+1) {
			return nil, fmt.Errorf("blockindex: path node %d has level %d, want %d", i, pn.Level, i+1)
		}
		if err := checkChildrenOrder(pn.Children); err != nil {
			return nil, fmt.Errorf("blockindex: path node %d: %w", i, err)
		}
		if pn.ChildIndex < 0 || pn.ChildIndex >= len(pn.Children) {
			return nil, fmt.Errorf("blockindex: path node %d: child index %d out of range", i, pn.ChildIndex)
		}
		// The on-path child info must equal what we computed from below:
		// this binds the claimed ranges and counts of the path itself.
		if onPath := pn.Children[pn.ChildIndex]; onPath != cur {
			return nil, fmt.Errorf("blockindex: path node %d does not commit to the child below", i)
		}
		var sumEntries, sumBlocks uint64
		for j, c := range pn.Children {
			if sumEntries, err = add64(sumEntries, c.Entries); err != nil {
				return nil, err
			}
			if sumBlocks, err = add64(sumBlocks, c.Blocks); err != nil {
				return nil, err
			}
			switch {
			case j < pn.ChildIndex:
				if ctx.leftEntries, err = add64(ctx.leftEntries, c.Entries); err != nil {
					return nil, err
				}
				// Left siblings are ordered, so the largest MaxKey seen is
				// the max key of the block immediately left of the path.
				if !ctx.hasPred || c.MaxKey > ctx.predMax {
					ctx.predMax, ctx.hasPred = c.MaxKey, true
				}
			case j > pn.ChildIndex:
				// The smallest MinKey among right siblings is the min key
				// of the block immediately right of the path.
				if !ctx.hasSucc || c.MinKey < ctx.succMin {
					ctx.succMin, ctx.hasSucc = c.MinKey, true
				}
			}
		}
		cur = ChildInfo{
			MinKey:  pn.Children[0].MinKey,
			MaxKey:  pn.Children[len(pn.Children)-1].MaxKey,
			Entries: sumEntries,
			Blocks:  sumBlocks,
			Digest:  NodeDigest(EncodeNode(pn.Version, pn.Level, pn.Children)),
		}
	}
	if cur.Digest != root.RootDigest {
		return nil, errors.New("blockindex: proof does not reach the root digest")
	}
	if cur.Entries != root.TotalEntries || cur.Blocks != root.TotalBlocks {
		return nil, errors.New("blockindex: proof totals disagree with root summary")
	}
	// The number of blocks committed left of the path is the leaf position;
	// it must match the claimed block index (position binding).
	leftBlocks := uint64(0)
	for _, pn := range bp.Path {
		for j := 0; j < pn.ChildIndex; j++ {
			if leftBlocks, err = add64(leftBlocks, pn.Children[j].Blocks); err != nil {
				return nil, err
			}
		}
	}
	if leftBlocks != bp.BlockIndex {
		return nil, fmt.Errorf("blockindex: claimed block index %d, proof commits to %d", bp.BlockIndex, leftBlocks)
	}
	return ctx, nil
}

// VerifyMembership verifies a point proof against the root summary. For a
// member it returns the value and the rank (aggregate count binding); for a
// non-member it requires the proof to commit to a gap containing the key.
func VerifyMembership(root RootSummary, key uint64, p *BlockProof) (*MembershipResult, error) {
	if root.TotalBlocks == 0 {
		if p == nil || p.Block != nil || len(p.Path) != 0 {
			return nil, errors.New("blockindex: expected empty proof for empty index")
		}
		return &MembershipResult{Key: key}, nil
	}
	ctx, err := verifyBlockPath(root, p)
	if err != nil {
		return nil, err
	}
	entries := ctx.entries
	i := sort.Search(len(entries), func(i int) bool { return entries[i].Key >= key })
	if i < len(entries) && entries[i].Key == key {
		return &MembershipResult{
			Member: true,
			Key:    key,
			Value:  entries[i].Value,
			Rank:   ctx.leftEntries + uint64(i),
		}, nil
	}
	lo := entries[0].Key
	hi := entries[len(entries)-1].Key
	switch {
	case key > lo && key < hi:
		// Inside the block's committed range but absent from its content.
	case key > hi:
		// Absent only if the committed successor starts beyond key, or
		// this is the last block of the index.
		if ctx.hasSucc && key >= ctx.succMin {
			return nil, errors.New("blockindex: proof does not exclude key (successor bound)")
		}
	case key < lo:
		if ctx.hasPred && key <= ctx.predMax {
			return nil, errors.New("blockindex: proof does not exclude key (predecessor bound)")
		}
	}
	return &MembershipResult{Key: key}, nil
}

// VerifySelect verifies a proof for the entry at the given rank and returns
// it. The rank is checked against the aggregate counts committed on the
// path, so a proof for the wrong position fails.
func VerifySelect(root RootSummary, rank uint64, p *BlockProof) (*Entry, error) {
	if root.TotalBlocks == 0 {
		return nil, errors.New("blockindex: select on empty index")
	}
	ctx, err := verifyBlockPath(root, p)
	if err != nil {
		return nil, err
	}
	if rank < ctx.leftEntries {
		return nil, errors.New("blockindex: rank precedes proven block")
	}
	local := rank - ctx.leftEntries
	if local >= uint64(len(ctx.entries)) {
		return nil, errors.New("blockindex: rank beyond proven block")
	}
	e := ctx.entries[local]
	return &e, nil
}

// VerifyRange verifies a range query certificate and returns exactly the
// entries in [lo, hi]. It fails unless the certificate proves that no
// committed block containing range keys was omitted: the returned blocks
// must be consecutive in the committed block order, the block before the
// first one must end below lo, and the block after the last one must start
// above hi.
func VerifyRange(root RootSummary, lo, hi uint64, rp *RangeProof) ([]Entry, error) {
	if lo > hi {
		return nil, errors.New("blockindex: empty query range")
	}
	if rp == nil {
		return nil, errors.New("blockindex: nil range proof")
	}
	if rp.Version != root.Version {
		return nil, fmt.Errorf("blockindex: proof version %d != root version %d", rp.Version, root.Version)
	}
	if root.TotalBlocks == 0 {
		if len(rp.Items) != 0 || rp.Boundary != nil {
			return nil, errors.New("blockindex: unexpected content for empty index")
		}
		return nil, nil
	}
	if len(rp.Items) == 0 {
		if rp.Boundary == nil {
			return nil, errors.New("blockindex: empty result without boundary proof")
		}
		ctx, err := verifyBlockPath(root, rp.Boundary)
		if err != nil {
			return nil, err
		}
		blo := ctx.entries[0].Key
		bhi := ctx.entries[len(ctx.entries)-1].Key
		below := bhi < lo && (!ctx.hasSucc || ctx.succMin > hi)
		above := blo > hi && (!ctx.hasPred || ctx.predMax < lo)
		if !below && !above {
			return nil, errors.New("blockindex: boundary block does not exclude the range")
		}
		return nil, nil
	}
	var out []Entry
	var prevIndex uint64
	for i := range rp.Items {
		ctx, err := verifyBlockPath(root, &rp.Items[i])
		if err != nil {
			return nil, fmt.Errorf("blockindex: range item %d: %w", i, err)
		}
		if i > 0 && rp.Items[i].BlockIndex != prevIndex+1 {
			return nil, errors.New("blockindex: range blocks are not consecutive (omission suspected)")
		}
		prevIndex = rp.Items[i].BlockIndex
		blo := ctx.entries[0].Key
		bhi := ctx.entries[len(ctx.entries)-1].Key
		if blo > hi || bhi < lo {
			return nil, fmt.Errorf("blockindex: range item %d lies outside the query range", i)
		}
		if i == 0 && ctx.hasPred && ctx.predMax >= lo {
			return nil, errors.New("blockindex: range may extend before the certificate")
		}
		if i == len(rp.Items)-1 && ctx.hasSucc && ctx.succMin <= hi {
			return nil, errors.New("blockindex: range may extend past the certificate")
		}
		for _, e := range ctx.entries {
			if e.Key >= lo && e.Key <= hi {
				out = append(out, e)
			}
		}
	}
	return out, nil
}

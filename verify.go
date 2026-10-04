package blkidx

import "fmt"

// VerifyError localizes a verification failure to the smallest bad node:
// level 0 is the leaf block, higher levels are internal summary nodes.
type VerifyError struct {
	IsLeaf bool
	Level  int
	NodeID uint32
	Reason string
}

func (e *VerifyError) Error() string {
	kind := "internal node"
	if e.IsLeaf {
		kind = "leaf block"
	}
	return fmt.Sprintf("verification failed at %s %d (level %d): %s", kind, e.NodeID, e.Level, e.Reason)
}

// VerifyStats records what a verification touched, to bound verifier I/O.
type VerifyStats struct {
	LeavesRead []uint32
	NodesRead  []uint32
}

func (s *VerifyStats) reset() {
	if s == nil {
		return
	}
	s.LeavesRead = nil
	s.NodesRead = nil
}

// checkedBlock is a leaf block that decoded successfully.
type checkedBlock struct {
	hdr     LeafHeader
	entries []Entry
	raw     []byte
}

// checkBlockPath authenticates one leaf block against the committed root:
// it verifies the leaf hash against its committing descriptor, then walks
// the internal path upward, recomputing every node hash and checking child
// order, offset chaining, key ranges and counts at each level. routeKey is
// the key used to confirm the path is the committed routing path (the query
// key for membership, the block's first key for range blocks).
func checkBlockPath(root [32]byte, block []byte, path []PathLevel, routeKey string, stats *VerifyStats) (*checkedBlock, *VerifyError) {
	hdr, entries, err := decodeLeaf(block)
	if err != nil {
		return nil, &VerifyError{IsLeaf: true, Level: 0, Reason: "leaf decode: " + err.Error()}
	}
	cb := &checkedBlock{hdr: hdr, entries: entries, raw: block}
	if stats != nil {
		stats.LeavesRead = append(stats.LeavesRead, hdr.BlockID)
	}
	if len(path) == 0 {
		if leafHash(block) != root {
			return nil, &VerifyError{IsLeaf: true, Level: 0, NodeID: hdr.BlockID,
				Reason: "leaf hash does not match committed root"}
		}
		return cb, nil
	}
	if verr := checkPathFrom(root, leafHash(block), cb, path, routeKey, stats); verr != nil {
		return nil, verr
	}
	return cb, nil
}

func checkPathFrom(root [32]byte, lh [32]byte, cb *checkedBlock, path []PathLevel, routeKey string, stats *VerifyStats) *VerifyError {
	hdr := cb.hdr
	// Level 0: leaf vs. first internal level's descriptor.
	lvl := path[0]
	if lvl.ChildIdx < 0 || lvl.ChildIdx >= len(lvl.Children) {
		return &VerifyError{Level: 1, NodeID: lvl.NodeID, Reason: "child index out of range"}
	}
	desc := lvl.Children[lvl.ChildIdx]
	if desc.Hash != lh {
		return &VerifyError{IsLeaf: true, Level: 0, NodeID: hdr.BlockID,
			Reason: "leaf hash does not match committing descriptor"}
	}
	lastKey := cb.entries[len(cb.entries)-1].Key
	if desc.BaseOff != hdr.BaseOff || uint64(desc.Count) != uint64(hdr.Count) || desc.MaxKey != lastKey {
		return &VerifyError{IsLeaf: true, Level: 0, NodeID: hdr.BlockID,
			Reason: fmt.Sprintf("leaf header (base=%d count=%d max=%q) does not match descriptor (base=%d count=%d max=%q)",
				hdr.BaseOff, hdr.Count, lastKey, desc.BaseOff, desc.Count, desc.MaxKey)}
	}

	// Internal levels, bottom-up.
	for i, lvl := range path {
		if stats != nil {
			stats.NodesRead = append(stats.NodesRead, lvl.NodeID)
		}
		if err := validateChildren(lvl.Children); err != nil {
			return &VerifyError{Level: i + 1, NodeID: lvl.NodeID, Reason: err.Error()}
		}
		if lvl.ChildIdx < 0 || lvl.ChildIdx >= len(lvl.Children) {
			return &VerifyError{Level: i + 1, NodeID: lvl.NodeID, Reason: "child index out of range"}
		}
		// The path must be the committed routing path for routeKey.
		want, inRange := routeChild(lvl.Children, routeKey)
		if want != lvl.ChildIdx {
			return &VerifyError{Level: i + 1, NodeID: lvl.NodeID,
				Reason: fmt.Sprintf("path descends into child %d but key %q routes to child %d", lvl.ChildIdx, routeKey, want)}
		}
		if !inRange && lvl.ChildIdx != len(lvl.Children)-1 {
			return &VerifyError{Level: i + 1, NodeID: lvl.NodeID,
				Reason: fmt.Sprintf("key %q exceeds committed range but path is not rightmost", routeKey)}
		}
		h := internalHash(lvl.Version, lvl.NodeID, lvl.Children)
		if i+1 < len(path) {
			up := path[i+1]
			if up.ChildIdx < 0 || up.ChildIdx >= len(up.Children) {
				return &VerifyError{Level: i + 2, NodeID: up.NodeID, Reason: "child index out of range"}
			}
			d := up.Children[up.ChildIdx]
			if d.Hash != h {
				return &VerifyError{Level: i + 1, NodeID: lvl.NodeID,
					Reason: "node hash does not match parent descriptor"}
			}
			if d.BaseOff != lvl.Children[0].BaseOff ||
				uint64(d.Count) != sumCounts(lvl.Children) ||
				d.MaxKey != lvl.Children[len(lvl.Children)-1].MaxKey {
				return &VerifyError{Level: i + 1, NodeID: lvl.NodeID,
					Reason: "node summary (base/count/maxKey) does not match parent descriptor"}
			}
		} else if h != root {
			return &VerifyError{Level: i + 1, NodeID: lvl.NodeID,
				Reason: "top-level node hash does not match committed root"}
		}
	}
	return nil
}

// pathBaseOff recomputes the committed global offset of the leaf reached by
// the path: the sum of the counts of all left siblings along the path.
func pathBaseOff(path []PathLevel) uint64 {
	var acc uint64
	for _, lvl := range path {
		for j := 0; j < lvl.ChildIdx; j++ {
			acc += uint64(lvl.Children[j].Count)
		}
	}
	return acc
}

// pathTotal returns the committed total entry count from the top level.
func pathTotal(path []PathLevel, leafCount uint32) uint64 {
	if len(path) == 0 {
		return uint64(leafCount)
	}
	return sumCounts(path[len(path)-1].Children)
}

// MembershipResult is the verified outcome of a membership proof.
type MembershipResult struct {
	Found bool
	Rank  uint64 // entries with smaller keys; the entry's position when Found
	Value []byte
}

// VerifyMembership checks a membership/absence proof against the committed
// root. It reads exactly one leaf block plus the internal path.
func VerifyMembership(root [32]byte, p *MembershipProof, stats *VerifyStats) (*MembershipResult, *VerifyError) {
	if stats != nil {
		stats.reset()
	}
	cb, verr := checkBlockPath(root, p.Leaf, p.Path, p.Key, stats)
	if verr != nil {
		return nil, verr
	}
	// Position binding: the leaf's committed base offset must equal the sum
	// of left-sibling counts along the path.
	if base := pathBaseOff(p.Path); base != cb.hdr.BaseOff {
		return nil, &VerifyError{IsLeaf: true, Level: 0, NodeID: cb.hdr.BlockID,
			Reason: fmt.Sprintf("leaf base offset %d inconsistent with path counts %d", cb.hdr.BaseOff, base)}
	}
	idx := lowerBound(cb.entries, p.Key)
	if p.Found {
		if idx >= len(cb.entries) || cb.entries[idx].Key != p.Key {
			return nil, &VerifyError{IsLeaf: true, Level: 0, NodeID: cb.hdr.BlockID,
				Reason: "claimed member key not present in committed block"}
		}
		if !equalBytes(cb.entries[idx].Value, p.Value) {
			return nil, &VerifyError{IsLeaf: true, Level: 0, NodeID: cb.hdr.BlockID,
				Reason: "claimed value does not match committed block content"}
		}
	} else if idx < len(cb.entries) && cb.entries[idx].Key == p.Key {
		return nil, &VerifyError{IsLeaf: true, Level: 0, NodeID: cb.hdr.BlockID,
			Reason: "claimed absent but key is present in committed block"}
	}
	rank := cb.hdr.BaseOff + uint64(idx)
	if rank != p.Rank {
		return nil, &VerifyError{IsLeaf: true, Level: 0, NodeID: cb.hdr.BlockID,
			Reason: fmt.Sprintf("claimed rank/position %d inconsistent with committed counts (%d)", p.Rank, rank)}
	}
	res := &MembershipResult{Found: p.Found, Rank: rank}
	if p.Found {
		res.Value = p.Value
	}
	return res, nil
}

// VerifySelect checks a proof that the entry at global position j is
// (proof.Key, proof.Value): position and value are bound together.
func VerifySelect(root [32]byte, j uint64, p *MembershipProof, stats *VerifyStats) (*MembershipResult, *VerifyError) {
	res, verr := VerifyMembership(root, p, stats)
	if verr != nil {
		return nil, verr
	}
	if !res.Found {
		return nil, &VerifyError{IsLeaf: true, Level: 0, Reason: "select proof claims absence"}
	}
	if res.Rank != j {
		return nil, &VerifyError{IsLeaf: true, Level: 0,
			Reason: fmt.Sprintf("select(%d) proved entry at position %d", j, res.Rank)}
	}
	return res, nil
}

// VerifyRange checks a range query certificate for [p.Lo, p.Hi] and returns
// the verified answer entries. Completeness: the verifier requires the
// provided blocks to form an unbroken, offset-chained sequence whose first
// block exhibits the predecessor of Lo (or is the committed first block)
// and whose last block exhibits the successor of Hi (or is the committed
// last block), so no block overlapping the range can be omitted.
func VerifyRange(root [32]byte, p *RangeProof, stats *VerifyStats) ([]Entry, *VerifyError) {
	if stats != nil {
		stats.reset()
	}
	if p.Lo > p.Hi {
		return nil, &VerifyError{Reason: "empty query range: lo > hi"}
	}
	if len(p.Blocks) == 0 || len(p.Blocks) != len(p.Paths) {
		return nil, &VerifyError{Reason: "certificate must carry at least one block and one path per block"}
	}

	blocks := make([]*checkedBlock, len(p.Blocks))
	for i, raw := range p.Blocks {
		hdr, entries, err := decodeLeaf(raw)
		if err != nil {
			return nil, &VerifyError{IsLeaf: true, Level: 0, Reason: "leaf decode: " + err.Error()}
		}
		routeKey := entries[0].Key
		cb, verr := checkBlockPath(root, raw, p.Paths[i], routeKey, stats)
		if verr != nil {
			return nil, verr
		}
		cb.hdr = hdr
		blocks[i] = cb
		// The block's committed base offset must match its path counts.
		if base := pathBaseOff(p.Paths[i]); base != cb.hdr.BaseOff {
			return nil, &VerifyError{IsLeaf: true, Level: 0, NodeID: cb.hdr.BlockID,
				Reason: fmt.Sprintf("leaf base offset %d inconsistent with path counts %d", cb.hdr.BaseOff, base)}
		}
	}

	// Consecutive blocks must chain by offset and by key order, leaving no
	// room for an omitted block between them.
	for i := 1; i < len(blocks); i++ {
		prev, cur := blocks[i-1], blocks[i]
		if cur.hdr.BaseOff != prev.hdr.BaseOff+uint64(prev.hdr.Count) {
			return nil, &VerifyError{IsLeaf: true, Level: 0, NodeID: cur.hdr.BlockID,
				Reason: fmt.Sprintf("block sequence not contiguous: base %d after %d+%d (a block may be omitted)",
					cur.hdr.BaseOff, prev.hdr.BaseOff, prev.hdr.Count)}
		}
		prevLast := prev.entries[len(prev.entries)-1].Key
		if cur.entries[0].Key <= prevLast {
			return nil, &VerifyError{IsLeaf: true, Level: 0, NodeID: cur.hdr.BlockID,
				Reason: "blocks overlap or are out of order"}
		}
	}

	// Boundary conditions exclude omitted blocks at the edges.
	first, last := blocks[0], blocks[len(blocks)-1]
	hasPred := first.entries[0].Key < p.Lo
	if !hasPred && first.hdr.BaseOff != 0 {
		return nil, &VerifyError{IsLeaf: true, Level: 0, NodeID: first.hdr.BlockID,
			Reason: "first block shows no predecessor of lo and is not the committed first block"}
	}
	total := pathTotal(p.Paths[len(p.Paths)-1], last.hdr.Count)
	hasSucc := last.entries[len(last.entries)-1].Key > p.Hi
	if !hasSucc && last.hdr.BaseOff+uint64(last.hdr.Count) != total {
		return nil, &VerifyError{IsLeaf: true, Level: 0, NodeID: last.hdr.BlockID,
			Reason: "last block shows no successor of hi and is not the committed last block"}
	}

	var answer []Entry
	for _, b := range blocks {
		for _, e := range b.entries {
			if e.Key >= p.Lo && e.Key <= p.Hi {
				answer = append(answer, e)
			}
		}
	}
	return answer, nil
}

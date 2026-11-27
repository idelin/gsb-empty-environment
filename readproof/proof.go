package readproof

import "sort"

// Proof is a merged inclusion proof for one read request. Ranges are the
// requested half-open block intervals; Leaves holds every requested block
// exactly once; Nodes holds every necessary sibling subtree commitment
// exactly once, and no other nodes.
type Proof struct {
	Ranges []Range
	Leaves []Node
	Nodes  []Node
}

// rangeCovers reports whether the sorted, disjoint ranges fully contain
// [start, end).
func rangeCovers(ranges []Range, start, end uint64) bool {
	for _, r := range ranges {
		if r.Start <= start && r.End >= end {
			return true
		}
	}
	return false
}

// rangeIntersects reports whether any range intersects [start, end).
func rangeIntersects(ranges []Range, start, end uint64) bool {
	for _, r := range ranges {
		if r.Start < end && start < r.End {
			return true
		}
	}
	return false
}

// expectedWalk collects the requested leaf indices and exactly the sibling
// subtree intervals needed to authenticate ranges against a tree of n
// leaves. A subtree that is fully covered contributes leaves directly and
// needs no internal node; a fully uncovered subtree contributes exactly one
// sibling commitment.
func expectedWalk(n uint64, ranges []Range) (map[uint64]bool, []Node) {
	leafSet := make(map[uint64]bool)
	var nodes []Node
	var walk func(start, end uint64)
	walk = func(start, end uint64) {
		if rangeCovers(ranges, start, end) {
			for i := start; i < end; i++ {
				leafSet[i] = true
			}
			return
		}
		if !rangeIntersects(ranges, start, end) {
			nodes = append(nodes, Node{Start: start, End: end})
			return
		}
		if end-start == 1 {
			leafSet[start] = true
			return
		}
		sp := start + splitPoint(end-start)
		walk(start, sp)
		walk(sp, end)
	}
	walk(0, n)
	return leafSet, nodes
}

// BuildProof generates the minimal merged proof for ranges against the full
// reference tree. Ranges must be sorted and disjoint.
func (t *Tree) BuildProof(ranges []Range) (*Proof, error) {
	if t.length == 0 {
		if len(ranges) != 0 {
			return nil, ErrRangeOutOfBounds
		}
		return &Proof{}, nil
	}
	n := NumLeaves(t.length)
	ranges, err := canonicalize(ranges, n, false)
	if err != nil {
		return nil, err
	}
	leafSet, expected := expectedWalk(n, ranges)
	p := &Proof{Ranges: ranges}
	for idx := range leafSet {
		found := t.findNode(idx, idx+1)
		data := append([]byte(nil), found.leafData...)
		p.Leaves = append(p.Leaves, Node{
			Start: idx,
			End:   idx + 1,
			Hash:  found.hash,
			Data:  data,
		})
	}
	sort.Slice(p.Leaves, func(i, j int) bool { return p.Leaves[i].Start < p.Leaves[j].Start })
	for _, want := range expected {
		found := t.findNode(want.Start, want.End)
		p.Nodes = append(p.Nodes, Node{
			Start: want.Start,
			End:   want.End,
			Hash:  found.hash,
		})
	}
	return p, nil
}

type nodeKey2 struct {
	start uint64
	end   uint64
}

// recomputeRoot rebuilds the root over [start, end) from the requested leaf
// hashes and the sibling commitments. total is the file's total leaf count
// and drives the non-power-of-two split structure. A commitment present for
// an exact sub-interval short-circuits recursion; every consumed commitment
// was already checked against the expected sibling set in Verify.
func recomputeRoot(start, end uint64, leafHash map[uint64][32]byte, nodeHash map[nodeKey2][32]byte) ([32]byte, error) {
	if end-start == 1 {
		if h, ok := leafHash[start]; ok {
			return h, nil
		}
		if h, ok := nodeHash[nodeKey2{start, end}]; ok {
			return h, nil
		}
		return [32]byte{}, ErrUnexpectedNode
	}
	if h, ok := nodeHash[nodeKey2{start, end}]; ok {
		return h, nil
	}
	sp := start + splitPoint(end-start)
	left, err := recomputeRoot(start, sp, leafHash, nodeHash)
	if err != nil {
		return [32]byte{}, err
	}
	right, err := recomputeRoot(sp, end, leafHash, nodeHash)
	if err != nil {
		return [32]byte{}, err
	}
	return innerHash(start, end, left, right), nil
}

// Verify checks the manifest signature and that proof authenticates exactly
// its declared ranges under the manifest root. On success it returns the
// verified block bytes keyed by block index.
//
// Verification is strict: every necessary sibling appears exactly once,
// extra ranges and duplicate leaves are rejected, leaf effective lengths
// must match the manifest length, and any root other than the manifest
// root fails. A proof built under an older root therefore cannot verify
// against a newer handle.
func Verify(manifest *Manifest, verifier Verifier, proof *Proof) (map[uint64][]byte, error) {
	if err := manifest.Verify(verifier); err != nil {
		return nil, err
	}
	n := NumLeaves(manifest.Length)
	if n == 0 {
		if len(proof.Leaves) != 0 || len(proof.Nodes) != 0 || len(proof.Ranges) != 0 {
			return nil, ErrProofMismatch
		}
		return map[uint64][]byte{}, nil
	}
	ranges, err := canonicalize(proof.Ranges, n, false)
	if err != nil {
		return nil, err
	}
	wantLeaves, wantNodes := expectedWalk(n, ranges)

	leafHash := make(map[uint64][32]byte, len(proof.Leaves))
	out := make(map[uint64][]byte, len(proof.Leaves))
	for _, leaf := range proof.Leaves {
		if leaf.End != leaf.Start+1 {
			return nil, ErrUnexpectedLeaf
		}
		if !wantLeaves[leaf.Start] {
			return nil, ErrUnexpectedLeaf
		}
		if _, dup := leafHash[leaf.Start]; dup {
			return nil, ErrUnexpectedLeaf
		}
		if uint64(len(leaf.Data)) != ValidLength(leaf.Start, manifest.Length) {
			return nil, ErrBadLeafLength
		}
		if got := LeafHash(leaf.Start, leaf.Data); got != leaf.Hash {
			return nil, ErrProofMismatch
		}
		leafHash[leaf.Start] = leaf.Hash
		out[leaf.Start] = append([]byte(nil), leaf.Data...)
	}
	if len(leafHash) != len(wantLeaves) {
		return nil, ErrUnexpectedLeaf
	}

	nodeHash := make(map[nodeKey2][32]byte, len(proof.Nodes))
	for _, node := range proof.Nodes {
		matched := false
		for _, want := range wantNodes {
			if want.Start == node.Start && want.End == node.End {
				matched = true
				break
			}
		}
		if !matched {
			return nil, ErrUnexpectedNode
		}
		// A sibling interval may be a single leaf (for example [2,3) when
		// leaf 2 is the only uncovered block); it still carries no data.
		if node.Data != nil {
			return nil, ErrBadNodeInterval
		}
		key := nodeKey2{node.Start, node.End}
		if _, dup := nodeHash[key]; dup {
			return nil, ErrUnexpectedNode
		}
		nodeHash[key] = node.Hash
	}
	if len(nodeHash) != len(wantNodes) {
		return nil, ErrUnexpectedNode
	}

	root, err := recomputeRoot(0, n, leafHash, nodeHash)
	if err != nil {
		return nil, err
	}
	if root != manifest.Root {
		return nil, ErrProofMismatch
	}
	return out, nil
}

package readproof

import "sort"

// ChangedLeaf is an explicit update input: the new effective bytes for one
// block index of the new version.
type ChangedLeaf struct {
	Index uint64
	Data  []byte
}

// UpdateProof builds a proof for ranges under next using only the previous
// proof, the changed leaves and the new signed manifest.
//
// A subtree from the old proof is reused only when all of the following
// hold: its interval is structurally identical in both trees, none of its
// leaves appears in changed, and it contains neither the old nor the new
// tail block. The last condition is what binds effective length: any block
// whose length can differ (the final block of either generation) must be
// presented as an explicit leaf and rehashed. A block removed by
// truncation can therefore never be resurrected when the file grows again.
func UpdateProof(previous, next *Manifest, verifier Verifier, oldProof *Proof, changed []ChangedLeaf, ranges []Range) (*Proof, error) {
	if _, err := Verify(previous, verifier, oldProof); err != nil {
		return nil, err
	}
	if next.FileID != previous.FileID {
		return nil, ErrManifestChain
	}
	if next.Version != previous.Version+1 || next.PreviousRoot != previous.Root {
		return nil, ErrManifestChain
	}

	newN := NumLeaves(next.Length)
	merged, err := MergeRanges(ranges, newN)
	if err != nil {
		return nil, err
	}

	changedMap := make(map[uint64][]byte, len(changed))
	for _, leaf := range changed {
		if leaf.Index >= newN {
			return nil, ErrBadChangedLeaf
		}
		if _, dup := changedMap[leaf.Index]; dup {
			return nil, ErrBadChangedLeaf
		}
		if uint64(len(leaf.Data)) != ValidLength(leaf.Index, next.Length) {
			return nil, ErrBadChangedLeaf
		}
		changedMap[leaf.Index] = append([]byte(nil), leaf.Data...)
	}

	oldN := NumLeaves(previous.Length)
	oldLeaves := make(map[uint64]Node, len(oldProof.Leaves))
	for _, leaf := range oldProof.Leaves {
		oldLeaves[leaf.Start] = leaf
	}
	oldNodes := make(map[uint64]Node, len(oldProof.Nodes))
	for _, node := range oldProof.Nodes {
		oldNodes[node.Start] = node
	}

	wantLeaves, wantNodes := expectedWalk(newN, merged)
	newLeaves := make(map[uint64]Node, len(wantLeaves))
	for index := range wantLeaves {
		if data, ok := changedMap[index]; ok {
			newLeaves[index] = Node{
				Start: index,
				End:   index + 1,
				Hash:  LeafHash(index, data),
				Data:  append([]byte(nil), data...),
			}
			continue
		}
		old, ok := oldLeaves[index]
		if !ok {
			return nil, ErrMissingLeafData
		}
		// An index beyond the old tree was removed by truncation; even if
		// the new file grew again it must be supplied as a changed leaf
		// rather than resurrected from the old handle.
		if index >= oldN || index >= newN {
			return nil, ErrStaleSubtree
		}
		if uint64(len(old.Data)) != ValidLength(index, next.Length) {
			return nil, ErrBadLeafLength
		}
		if LeafHash(index, old.Data) != old.Hash {
			return nil, ErrProofMismatch
		}
		newLeaves[index] = Node{
			Start: index,
			End:   index + 1,
			Hash:  old.Hash,
			Data:  append([]byte(nil), old.Data...),
		}
	}

	newNodes := make(map[uint64]Node, len(wantNodes))
	for _, want := range wantNodes {
		hash, err := siblingHash(want.Start, want.End, oldN, newN, changedMap, oldNodes, oldLeaves, next.Length)
		if err != nil {
			return nil, err
		}
		newNodes[want.Start] = Node{Start: want.Start, End: want.End, Hash: hash}
	}

	proof := &Proof{Ranges: merged}
	for _, leaf := range newLeaves {
		proof.Leaves = append(proof.Leaves, leaf)
	}
	sort.Slice(proof.Leaves, func(i, j int) bool { return proof.Leaves[i].Start < proof.Leaves[j].Start })
	for _, node := range newNodes {
		proof.Nodes = append(proof.Nodes, node)
	}
	sort.Slice(proof.Nodes, func(i, j int) bool { return proof.Nodes[i].Start < proof.Nodes[j].Start })

	// Self-check: the assembled proof must verify under the new manifest.
	if _, err := Verify(next, verifier, proof); err != nil {
		return nil, err
	}
	return proof, nil
}

// siblingHash resolves the commitment of a needed sibling interval
// [start,end). A commitment carried by the old proof is reused only when
// the interval is identical in both trees, contains no changed leaf and
// contains neither the old nor the new tail block (so every index,
// interval and effective leaf length is provably unchanged). Otherwise the
// subtree is rebuilt from explicit changed leaves and authenticated old
// leaves; any missing leaf or changed effective length is an error, which
// also prevents a block removed by truncation from being resurrected.
func siblingHash(start, end, oldN, newN uint64, changed map[uint64][]byte, oldNodes map[uint64]Node, oldLeaves map[uint64]Node, newLength uint64) ([32]byte, error) {
	if end > newN {
		return [32]byte{}, ErrBadNodeInterval
	}
	containsChange := false
	for index := start; index < end; index++ {
		if _, ok := changed[index]; ok {
			containsChange = true
			break
		}
	}
	containsOldTail := oldN > 0 && end == oldN
	containsNewTail := newN > 0 && end == newN
	// Direct commitment reuse: identical structural interval, unchanged
	// leaves, and no tail block of either generation.
	if !containsChange && !containsOldTail && !containsNewTail && end <= oldN {
		if old, ok := oldNodes[start]; ok && old.End == end {
			return old.Hash, nil
		}
	}
	// Reconstruct from authenticated leaf material.
	leafHashes := make(map[uint64][32]byte, end-start)
	for index := start; index < end; index++ {
		if data, ok := changed[index]; ok {
			leafHashes[index] = LeafHash(index, data)
			continue
		}
		old, ok := oldLeaves[index]
		if !ok || index >= oldN {
			return [32]byte{}, ErrMissingLeafData
		}
		if uint64(len(old.Data)) != ValidLength(index, newLength) {
			return [32]byte{}, ErrBadLeafLength
		}
		if LeafHash(index, old.Data) != old.Hash {
			return [32]byte{}, ErrProofMismatch
		}
		leafHashes[index] = old.Hash
	}
	return hashRegion(start, end, leafHashes), nil
}

// hashRegion recomputes a subtree hash from authenticated leaf hashes.
func hashRegion(start, end uint64, leaves map[uint64][32]byte) [32]byte {
	if end-start == 1 {
		return leaves[start]
	}
	sp := start + splitPoint(end-start)
	left := hashRegion(start, sp, leaves)
	right := hashRegion(sp, end, leaves)
	return innerHash(start, end, left, right)
}

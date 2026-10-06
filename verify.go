package integrity

import (
	"bytes"
	"crypto/subtle"
)

// leafFor returns the leaf hash for a block: the explicit empty leaf for
// a missing block (nil hash), or the position- and count-bound leaf hash
// for a present block.
func leafFor(index, total uint64, blockHash []byte) []byte {
	if blockHash == nil {
		return emptyLeafHash(index, total)
	}
	return leafHash(index, total, blockHash)
}

// verifyPath folds a leaf and its sibling path up to a root candidate and
// compares it, in constant time, with want.
func verifyPath(want []byte, index, total uint64, blockHash []byte, siblings [][]byte) bool {
	if total == 0 || index >= total {
		return false
	}
	if len(siblings) != depthFor(total) {
		return false
	}
	if blockHash != nil && len(blockHash) != HashSize {
		return false
	}
	cur := leafFor(index, total, blockHash)
	pos := index
	for _, sib := range siblings {
		if len(sib) != HashSize {
			return false
		}
		if pos&1 == 0 {
			cur = nodeHash(cur, sib)
		} else {
			cur = nodeHash(sib, cur)
		}
		pos >>= 1
	}
	return subtle.ConstantTimeCompare(cur, want) == 1
}

// VerifyProof checks a single-block membership proof against root.
func VerifyProof(root []byte, p Proof) bool {
	if p.Missing {
		if p.BlockHash != nil {
			return false
		}
	} else if p.BlockHash == nil {
		return false
	}
	return verifyPath(root, p.Index, p.Total, p.BlockHash, p.Siblings)
}

// VerifyBatchProof checks a multi-block proof against root. Every shared
// path node is recomputed from the proven leaves; a provided node that
// conflicts with a recomputed one, or any missing frontier node, fails
// verification. Missing blocks (nil entries in Blocks) are verified as
// explicit empty leaves and cannot be swapped with zero-valued blocks.
func VerifyBatchProof(root []byte, bp BatchProof) bool {
	if bp.Total == 0 || len(bp.Indices) == 0 || len(bp.Indices) != len(bp.Blocks) {
		return false
	}
	computed := make(map[uint64][]byte, len(bp.Indices))
	positions := make([]uint64, len(bp.Indices))
	for i, idx := range bp.Indices {
		if idx >= bp.Total {
			return false
		}
		if i > 0 && idx <= bp.Indices[i-1] { // must be strictly increasing
			return false
		}
		if bp.Blocks[i] != nil && len(bp.Blocks[i]) != HashSize {
			return false
		}
		computed[nodeID(0, idx)] = leafFor(idx, bp.Total, bp.Blocks[i])
		positions[i] = idx
	}

	depth := depthFor(bp.Total)
	for level := 0; level < depth; level++ {
		seen := make(map[uint64]bool, len(positions))
		next := make([]uint64, 0, len(positions))
		for _, pos := range positions {
			sibID := nodeID(uint64(level), pos^1)
			sib, ok := computed[sibID]
			if provided, given := bp.Nodes[sibID]; given {
				if len(provided) != HashSize {
					return false
				}
				// Shared path nodes must be consistent: a node the
				// verifier can recompute itself must match the proof.
				if ok && !bytes.Equal(sib, provided) {
					return false
				}
				sib = provided
				ok = true
			}
			if !ok {
				return false
			}
			cur := computed[nodeID(uint64(level), pos)]
			var parent []byte
			if pos&1 == 0 {
				parent = nodeHash(cur, sib)
			} else {
				parent = nodeHash(sib, cur)
			}
			p := pos >> 1
			if !seen[p] {
				seen[p] = true
				next = append(next, p)
			}
			computed[nodeID(uint64(level+1), p)] = parent
		}
		positions = next
	}

	got, ok := computed[nodeID(uint64(depth), 0)]
	if !ok {
		return false
	}
	// Every provided node that the verifier can recompute by itself must
	// agree with the recomputed value: shared path nodes must be
	// consistent across all proven blocks.
	for id, provided := range bp.Nodes {
		if c, ok := computed[id]; ok && !bytes.Equal(c, provided) {
			return false
		}
	}
	return subtle.ConstantTimeCompare(got, root) == 1
}

// VerifyUpdateProof checks that the update proof binds OldRoot and
// NewRoot of a snapshot with Total blocks through a change confined to
// the single block at Index: both paths must recompute to their
// respective roots, and every shared path node must be identical between
// the old and the new tree.
func VerifyUpdateProof(up UpdateProof) bool {
	if up.Total == 0 || up.Index >= up.Total {
		return false
	}
	depth := depthFor(up.Total)
	if len(up.OldSiblings) != depth || len(up.NewSiblings) != depth {
		return false
	}
	for i := range up.OldSiblings {
		if !bytes.Equal(up.OldSiblings[i], up.NewSiblings[i]) {
			return false
		}
	}
	if !verifyPath(up.OldRoot, up.Index, up.Total, up.OldBlock, up.OldSiblings) {
		return false
	}
	if !verifyPath(up.NewRoot, up.Index, up.Total, up.NewBlock, up.NewSiblings) {
		return false
	}
	return true
}

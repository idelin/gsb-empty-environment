package snapverify

import (
	"crypto/sha256"
	"encoding/binary"
)

// HashSize is the byte length of a Merkle node hash.
const HashSize = sha256.Size

// Hash is a Merkle node hash.
type Hash [HashSize]byte

// Domain separation tags.
const (
	tagEmptyLeaf = 0x00
	tagLeaf      = 0x01
	tagInterior  = 0x02
)

// emptyLeafHash is the hash committed for an unoccupied key.
func emptyLeafHash() Hash {
	return sha256.Sum256([]byte{tagEmptyLeaf})
}

// leafHash commits an occupied key to its value (block content).
func leafHash(key uint64, value []byte) Hash {
	var kb [8]byte
	binary.BigEndian.PutUint64(kb[:], key)
	buf := make([]byte, 0, 1+8+len(value))
	buf = append(buf, tagLeaf)
	buf = append(buf, kb[:]...)
	buf = append(buf, value...)
	return sha256.Sum256(buf)
}

// interiorHash commits an interior node to its two children.
func interiorHash(left, right Hash) Hash {
	var buf [1 + 2*HashSize]byte
	buf[0] = tagInterior
	copy(buf[1:1+HashSize], left[:])
	copy(buf[1+HashSize:], right[:])
	return sha256.Sum256(buf[:])
}

// emptyHashes returns hashes of fully empty subtrees, indexed by height
// (0 = empty leaf, depth = empty root).
func emptyHashes(depth int) []Hash {
	out := make([]Hash, depth+1)
	out[0] = emptyLeafHash()
	for i := 1; i <= depth; i++ {
		out[i] = interiorHash(out[i-1], out[i-1])
	}
	return out
}

// nodePos identifies a tree node. Level 0 is the leaf level; the root sits
// at level == depth, index 0.
type nodePos struct {
	level uint32
	index uint64
}

// requiredPositions computes the exact set of sibling nodes a verifier needs
// from a proof in order to recompute the root when only the given leaf keys
// are known. Everything outside these positions is determined by the leaves
// themselves.
func requiredPositions(depth int, keys []uint64) map[nodePos]struct{} {
	required := make(map[nodePos]struct{})
	if len(keys) == 0 {
		return required
	}
	cur := make(map[nodePos]struct{}, len(keys))
	for _, k := range keys {
		cur[nodePos{level: 0, index: k}] = struct{}{}
	}
	for lvl := uint32(0); lvl < uint32(depth); lvl++ {
		next := make(map[nodePos]struct{}, len(cur))
		for pos := range cur {
			sib := nodePos{level: lvl, index: pos.index ^ 1}
			if _, ok := cur[sib]; !ok {
				required[sib] = struct{}{}
			}
			next[nodePos{level: lvl + 1, index: pos.index >> 1}] = struct{}{}
		}
		cur = next
	}
	return required
}

// computeRoot folds the given leaf hashes up to the root, pulling sibling
// hashes from nodes wherever a subtree is not covered by the leaves.
func computeRoot(depth int, leaves map[uint64]Hash, nodes map[nodePos]Hash) (Hash, error) {
	cur := make(map[nodePos]Hash, len(leaves))
	for k, h := range leaves {
		cur[nodePos{level: 0, index: k}] = h
	}
	for lvl := uint32(0); lvl < uint32(depth); lvl++ {
		next := make(map[nodePos]Hash, len(cur))
		for pos, h := range cur {
			sibPos := nodePos{level: lvl, index: pos.index ^ 1}
			sib, ok := cur[sibPos]
			if !ok {
				sib, ok = nodes[sibPos]
				if !ok {
					return Hash{}, &ErrMissingNode{Level: sibPos.level, Index: sibPos.index}
				}
			}
			var parent Hash
			if pos.index%2 == 0 {
				parent = interiorHash(h, sib)
			} else {
				parent = interiorHash(sib, h)
			}
			next[nodePos{level: lvl + 1, index: pos.index >> 1}] = parent
		}
		cur = next
	}
	root, ok := cur[nodePos{level: uint32(depth), index: 0}]
	if !ok {
		return Hash{}, errNoLeaves
	}
	return root, nil
}

// Package integrity implements a binary digest (Merkle) tree over a fixed
// number of data blocks. Blocks are addressed by their fixed index; blocks
// that have never been written are represented by explicit empty leaves.
// The leaf level is padded with empty leaves up to the next power of two.
//
// All hashes are domain separated and every leaf commits to its block index
// and the total block count, so a leaf from one position (or from a tree
// with a different block count) can never be replayed as another block.
package integrity

import (
	"crypto/sha256"
	"encoding/binary"
)

// HashSize is the size in bytes of a digest-tree node hash.
const HashSize = sha256.Size

// Hash is a node hash in the digest tree.
type Hash [HashSize]byte

// Domain separation tags for the different hashable node kinds.
const (
	tagLeafData  byte = 0x00 // leaf holding a present (possibly all-zero) block
	tagLeafEmpty byte = 0x01 // explicit empty leaf for a missing block
	tagInterior  byte = 0x02 // interior node
)

// leafHashData hashes a present block. The block index and the total block
// count are committed so the leaf is bound to its position and snapshot
// shape. An all-zero block still hashes differently from an empty leaf
// because a different domain tag is used.
func leafHashData(index, total uint64, data []byte) Hash {
	h := sha256.New()
	h.Write([]byte{tagLeafData})
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], index)
	h.Write(buf[:])
	binary.BigEndian.PutUint64(buf[:], total)
	h.Write(buf[:])
	h.Write(data)
	var out Hash
	copy(out[:], h.Sum(nil))
	return out
}

// leafHashEmpty hashes the explicit empty leaf for a missing block at the
// given index. It is distinct from any data leaf, including a zero-valued
// block, and from the empty leaf at any other index or block count.
func leafHashEmpty(index, total uint64) Hash {
	h := sha256.New()
	h.Write([]byte{tagLeafEmpty})
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], index)
	h.Write(buf[:])
	binary.BigEndian.PutUint64(buf[:], total)
	h.Write(buf[:])
	var out Hash
	copy(out[:], h.Sum(nil))
	return out
}

// interiorHash hashes an interior node from its left and right children.
func interiorHash(left, right Hash) Hash {
	h := sha256.New()
	h.Write([]byte{tagInterior})
	h.Write(left[:])
	h.Write(right[:])
	var out Hash
	copy(out[:], h.Sum(nil))
	return out
}

// paddedSize returns the leaf-count of the tree: the smallest power of two
// that is >= total, with a minimum of 1 so the empty file has a well
// defined root.
func paddedSize(total uint64) uint64 {
	size := uint64(1)
	for size < total {
		size <<= 1
	}
	return size
}

// depthOf returns the number of interior levels for the given block count.
func depthOf(total uint64) int {
	depth := 0
	for size := paddedSize(total); size > 1; size >>= 1 {
		depth++
	}
	return depth
}

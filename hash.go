package netsec

import (
	"crypto/sha256"
	"encoding/binary"
	"hash"
)

const (
	// HashSize is the byte length of node hashes and VRF outputs.
	HashSize = 32
	// IndexBits is the number of VRF output bits walked through the tree.
	IndexBits = 256
)

// Domain separation tags: internal nodes, leaves and empty subtrees are
// hashed under distinct tags so a node of one kind can never be
// reinterpreted as another kind.
var (
	domainLeaf  = []byte("netsec-kt-leaf-v1")
	domainNode  = []byte("netsec-kt-node-v1")
	domainEmpty = []byte("netsec-kt-empty-v1")
)

func writeDepth(h hash.Hash, depth int) {
	var b [2]byte
	binary.BigEndian.PutUint16(b[:], uint16(depth))
	h.Write(b[:])
}

// hashLeaf commits to a leaf at the given depth holding (index, value).
func hashLeaf(depth int, index, value []byte) []byte {
	h := sha256.New()
	h.Write(domainLeaf)
	writeDepth(h, depth)
	h.Write(index)
	h.Write(value)
	return h.Sum(nil)
}

// hashNode commits to an internal node at the given depth with the given
// left and right children.
func hashNode(depth int, left, right []byte) []byte {
	h := sha256.New()
	h.Write(domainNode)
	writeDepth(h, depth)
	h.Write(left)
	h.Write(right)
	return h.Sum(nil)
}

// hashEmpty commits to an empty subtree rooted at the given depth.
func hashEmpty(depth int) []byte {
	h := sha256.New()
	h.Write(domainEmpty)
	writeDepth(h, depth)
	return h.Sum(nil)
}

// bit returns the i-th bit of index, MSB first. Bit i selects the child
// descended into at tree depth i.
func bit(index []byte, i int) byte {
	return (index[i/8] >> uint(7-i%8)) & 1
}

// prefixEqual reports whether a and b agree on their first n bits.
func prefixEqual(a, b []byte, n int) bool {
	for i := 0; i < n; i++ {
		if bit(a, i) != bit(b, i) {
			return false
		}
	}
	return true
}

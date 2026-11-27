// Package readproof implements importable read-proof verification for a
// content-addressed block service.
//
// Files are split into fixed BlockSize blocks. Each leaf hash is bound to a
// domain tag, the block index, the effective block length and the block
// bytes. Each internal hash is bound to a domain tag, the covered leaf
// interval and the two child hashes. Non-power-of-two regions are split at
// the largest power of two strictly below the region length, and the empty
// file has its own standalone commitment (EmptyRoot).
//
// A signed Manifest binds a file identity to its length, monotonically
// increasing version, the previous-version root and the current-version
// root. A Proof authenticates one read spanning multiple, potentially
// non-contiguous, block ranges. Verify enforces that every necessary
// sibling appears exactly once and rejects extra ranges, duplicate leaves
// and roots that do not match the supplied manifest.
package readproof

import (
	"crypto/sha256"
	"errors"
)

// BlockSize is the fixed block size in bytes.
const BlockSize = 4096

// Domain separation tags. Tags are length-prefixed inside the hashed input
// so that no concatenation of encoded fields can collide with a tag value.
var (
	leafDomain     = []byte("readproof.leaf")
	innerDomain    = []byte("readproof.inner")
	emptyDomain    = []byte("readproof.empty")
	manifestDomain = []byte("readproof.manifest")
)

// Verification and construction errors.
var (
	ErrBadSignature     = errors.New("readproof: manifest signature verification failed")
	ErrEmptyRange       = errors.New("readproof: empty range")
	ErrRangeUnordered   = errors.New("readproof: ranges must be sorted and disjoint")
	ErrRangeOutOfBounds = errors.New("readproof: range outside file")
	ErrProofMismatch    = errors.New("readproof: proof does not match manifest root")
	ErrUnexpectedLeaf   = errors.New("readproof: unexpected or duplicate leaf in proof")
	ErrUnexpectedNode   = errors.New("readproof: unexpected or duplicate sibling node in proof")
	ErrBadLeafLength    = errors.New("readproof: leaf has wrong effective length")
	ErrBadNodeInterval  = errors.New("readproof: sibling node has wrong interval")
	ErrBadChangedLeaf   = errors.New("readproof: invalid or duplicate changed leaf")
	ErrMissingLeafData  = errors.New("readproof: required leaf data missing from update inputs")
	ErrManifestChain    = errors.New("readproof: manifest previous root or version does not chain")
	ErrStaleSubtree     = errors.New("readproof: stale subtree cannot be reused after truncation")
)

func writeUint64(buf []byte, v uint64) {
	for i := 7; i >= 0; i-- {
		buf[i] = byte(v)
		v >>= 8
	}
}

func appendString(b, s []byte) []byte {
	var lenBuf [8]byte
	writeUint64(lenBuf[:], uint64(len(s)))
	b = append(b, lenBuf[:]...)
	return append(b, s...)
}

func appendUint64(b []byte, v uint64) []byte {
	var lenBuf [8]byte
	writeUint64(lenBuf[:], v)
	return append(b, lenBuf[:]...)
}

// LeafHash computes the leaf commitment for the block at index using the
// supplied effective bytes. len(data) must be between 1 and BlockSize. The
// hash binds the domain tag, block index, effective length and bytes.
func LeafHash(index uint64, data []byte) [32]byte {
	if len(data) == 0 || len(data) > BlockSize {
		panic("readproof: leaf data length out of range")
	}
	h := sha256.New()
	h.Write(appendString(nil, leafDomain))
	h.Write(appendUint64(nil, index))
	h.Write(appendUint64(nil, uint64(len(data))))
	h.Write(data)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func innerHash(start, end uint64, left, right [32]byte) [32]byte {
	h := sha256.New()
	h.Write(appendString(nil, innerDomain))
	h.Write(appendUint64(nil, start))
	h.Write(appendUint64(nil, end))
	h.Write(left[:])
	h.Write(right[:])
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// EmptyRoot is the standalone commitment of a zero-length file.
var EmptyRoot = func() [32]byte {
	h := sha256.New()
	h.Write(appendString(nil, emptyDomain))
	h.Write(appendUint64(nil, 0))
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}()

// NumLeaves returns the number of blocks a file of the given length occupies.
func NumLeaves(length uint64) uint64 {
	return (length + BlockSize - 1) / BlockSize
}

// ValidLength returns the effective length of block index inside a file of
// length. Every block except possibly the last holds BlockSize bytes.
func ValidLength(index, length uint64) uint64 {
	if length == 0 {
		panic("readproof: empty file has no leaves")
	}
	start := index * BlockSize
	if start >= length {
		panic("readproof: block index outside file")
	}
	remaining := length - start
	if remaining < BlockSize {
		return remaining
	}
	return BlockSize
}

// splitPoint returns the largest power of two strictly smaller than n. The
// region [0,n) is therefore split into [0,sp) and [sp,n) where the left
// child is a complete binary tree.
func splitPoint(n uint64) uint64 {
	if n <= 1 {
		panic("readproof: split of region smaller than two leaves")
	}
	sp := uint64(1)
	for sp<<1 < n {
		sp <<= 1
	}
	return sp
}

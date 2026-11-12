package netsec

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// HashSize is the size, in bytes, of the hash used by this package.
const HashSize = sha256.Size

// Hash is a SHA-256 digest.
type Hash [HashSize]byte

// String returns the lowercase hexadecimal representation of h.
func (h Hash) String() string {
	return hex.EncodeToString(h[:])
}

// IsZero reports whether h is the all-zero digest.
func (h Hash) IsZero() bool {
	return h == Hash{}
}

// ParseHash parses a 32-byte hexadecimal SHA-256 digest.
func ParseHash(s string) (Hash, error) {
	var h Hash
	if len(s) != hex.EncodedLen(len(h)) {
		return h, fmt.Errorf("netsec: hash must be %d hexadecimal characters", hex.EncodedLen(len(h)))
	}
	if _, err := hex.Decode(h[:], []byte(s)); err != nil {
		return Hash{}, fmt.Errorf("netsec: parse hash: %w", err)
	}
	return h, nil
}

var emptyRoot = sha256.Sum256(nil)

// EmptyRoot is the RFC 9162 hash of an empty tree: SHA-256("").
var EmptyRoot = Hash(emptyRoot)

// LeafHash returns SHA-256(0x00 || data), the RFC 9162 leaf hash.
func LeafHash(data []byte) Hash {
	return hashWithPrefix(0x00, data)
}

// NodeHash returns SHA-256(0x01 || left || right), the RFC 9162 interior
// node hash.
func NodeHash(left, right Hash) Hash {
	h := sha256.New()
	h.Write([]byte{0x01})
	h.Write(left[:])
	h.Write(right[:])
	var out Hash
	h.Sum(out[:0])
	return out
}

func hashWithPrefix(prefix byte, data []byte) Hash {
	h := sha256.New()
	h.Write([]byte{prefix})
	h.Write(data)
	var out Hash
	h.Sum(out[:0])
	return out
}

// MerkleRoot computes the RFC 9162 Merkle tree root for leaves. It returns
// EmptyRoot when leaves is empty.
func MerkleRoot(leaves [][]byte) Hash {
	hashes := make([]Hash, len(leaves))
	for i := range leaves {
		hashes[i] = LeafHash(leaves[i])
	}
	return RootFromHashes(hashes)
}

// RootFromHashes computes the RFC 9162 Merkle tree root for already hashed
// leaves. It returns EmptyRoot when hashes is empty.
func RootFromHashes(hashes []Hash) Hash {
	if len(hashes) == 0 {
		return EmptyRoot
	}
	return subtreeRoot(hashes)
}

func subtreeRoot(hashes []Hash) Hash {
	if len(hashes) == 1 {
		return hashes[0]
	}
	k := splitPoint(uint64(len(hashes)))
	return NodeHash(subtreeRoot(hashes[:k]), subtreeRoot(hashes[k:]))
}

// splitPoint returns the largest power of two strictly less than n for n > 1.
func splitPoint(n uint64) uint64 {
	k := uint64(1)
	for k <= (n-1)/2 {
		k <<= 1
	}
	return k
}

// ProveInclusion returns the RFC 9162 inclusion proof for the leaf at index.
// The proof is ordered from the leaf level upwards; each element is the root
// of a sibling subtree.
func ProveInclusion(leaves [][]byte, index uint64) ([]Hash, error) {
	hashes := make([]Hash, len(leaves))
	for i := range leaves {
		hashes[i] = LeafHash(leaves[i])
	}
	return ProveInclusionFromHashes(hashes, index)
}

// ProveInclusionFromHashes is like ProveInclusion but accepts already hashed
// leaves.
func ProveInclusionFromHashes(hashes []Hash, index uint64) ([]Hash, error) {
	if uint64(index) >= uint64(len(hashes)) {
		return nil, fmt.Errorf("%w: block %d is outside tree of length %d", ErrBlockRange, index, len(hashes))
	}
	return proveHashes(hashes, index), nil
}

func proveHashes(hashes []Hash, index uint64) []Hash {
	if len(hashes) == 1 {
		return nil
	}
	k := splitPoint(uint64(len(hashes)))
	if index < k {
		proof := proveHashes(hashes[:k], index)
		return append(proof, subtreeRoot(hashes[k:]))
	}
	proof := proveHashes(hashes[k:], index-k)
	return append(proof, subtreeRoot(hashes[:k]))
}

// VerifyInclusion reports whether data at index, together with proof, folds
// up to root for a tree of the given size under RFC 9162 hashing rules.
func VerifyInclusion(root Hash, size, index uint64, data []byte, proof []Hash) bool {
	return VerifyInclusionHash(root, size, index, LeafHash(data), proof)
}

// VerifyInclusionHash is like VerifyInclusion but accepts an already computed
// leaf hash.
func VerifyInclusionHash(root Hash, size, index uint64, leaf Hash, proof []Hash) bool {
	if size == 0 || index >= size {
		return false
	}
	computed, remainder, ok := foldInclusion(index, size, leaf, proof)
	return ok && len(remainder) == 0 && computed == root
}

func foldInclusion(index, size uint64, leaf Hash, proof []Hash) (Hash, []Hash, bool) {
	if size == 0 || index >= size {
		return Hash{}, proof, false
	}
	if size == 1 {
		return leaf, proof, true
	}

	k := splitPoint(size)
	if index < k {
		child, remainder, ok := foldInclusion(index, k, leaf, proof)
		if !ok || len(remainder) == 0 {
			return Hash{}, proof, false
		}
		return NodeHash(child, remainder[0]), remainder[1:], true
	}

	child, remainder, ok := foldInclusion(index-k, size-k, leaf, proof)
	if !ok || len(remainder) == 0 {
		return Hash{}, proof, false
	}
	return NodeHash(remainder[0], child), remainder[1:], true
}

package admission

import (
	"bytes"
	"fmt"
)

// Tree is an RFC 6962-style Merkle tree. Leaves are supplied as raw payloads
// and are hashed with HashLeaf. Subtree hashes are cached so that many proofs
// can be generated from one tree.
type Tree struct {
	hashes [][]byte
	cache  map[subtreeKey][]byte
}

type subtreeKey struct {
	start int
	size  int
}

// NewTree constructs a tree from raw leaf payloads.
func NewTree(leaves [][]byte) *Tree {
	t := &Tree{
		hashes: make([][]byte, len(leaves)),
		cache:  make(map[subtreeKey][]byte),
	}
	for i, leaf := range leaves {
		t.hashes[i] = HashLeaf(leaf)
	}
	return t
}

// Size returns the number of leaves.
func (t *Tree) Size() uint64 { return uint64(len(t.hashes)) }

// LeafHash returns a copy of the hashed leaf at index.
func (t *Tree) LeafHash(index uint64) ([]byte, error) {
	if index >= uint64(len(t.hashes)) {
		return nil, fmt.Errorf("leaf index %d out of range for size %d", index, len(t.hashes))
	}
	return cloneBytes(t.hashes[index]), nil
}

// Root returns the Merkle root of the tree.
func (t *Tree) Root() []byte {
	return t.subtree(0, len(t.hashes))
}

// InclusionProof returns the Merkle inclusion path for a leaf. The returned
// path is ordered from the leaf level toward the root.
func (t *Tree) InclusionProof(index uint64) ([][]byte, error) {
	if index >= uint64(len(t.hashes)) {
		return nil, fmt.Errorf("leaf index %d out of range for size %d", index, len(t.hashes))
	}
	return t.inclusionPath(0, len(t.hashes), int(index)), nil
}

// ConsistencyProof returns a consistency proof from oldSize to newSize. Both
// sizes must be within the tree, and oldSize must not exceed newSize.
func (t *Tree) ConsistencyProof(oldSize, newSize uint64) ([][]byte, error) {
	if oldSize > newSize {
		return nil, fmt.Errorf("old size %d exceeds new size %d", oldSize, newSize)
	}
	if newSize > uint64(len(t.hashes)) {
		return nil, fmt.Errorf("new size %d exceeds tree size %d", newSize, len(t.hashes))
	}
	if oldSize == 0 {
		return nil, nil
	}
	return t.consistencySubproof(0, int(oldSize), int(newSize), true), nil
}

func (t *Tree) subtree(start, size int) []byte {
	if size == 0 {
		return EmptyRoot()
	}
	if size == 1 {
		return cloneBytes(t.hashes[start])
	}
	key := subtreeKey{start: start, size: size}
	if h, ok := t.cache[key]; ok {
		return cloneBytes(h)
	}
	k := splitPoint(size)
	left := t.subtree(start, k)
	right := t.subtree(start+k, size-k)
	h := HashNode(left, right)
	t.cache[key] = h
	return cloneBytes(h)
}

func (t *Tree) inclusionPath(start, size, index int) [][]byte {
	if size == 1 {
		return nil
	}
	k := splitPoint(size)
	if index < k {
		path := t.inclusionPath(start, k, index)
		return append(path, t.subtree(start+k, size-k))
	}
	path := t.inclusionPath(start+k, size-k, index-k)
	return append(path, t.subtree(start, k))
}

func (t *Tree) consistencySubproof(start, oldSize, newSize int, complete bool) [][]byte {
	if oldSize == newSize {
		if complete {
			return nil
		}
		return [][]byte{t.subtree(start, newSize)}
	}
	k := splitPoint(newSize)
	if oldSize <= k {
		path := t.consistencySubproof(start, oldSize, k, complete)
		return append(path, t.subtree(start+k, newSize-k))
	}
	path := t.consistencySubproof(start+k, oldSize-k, newSize-k, false)
	return append(path, t.subtree(start, k))
}

// splitPoint returns the largest power of two strictly smaller than n. For
// n <= 1 the caller handles the singleton case before calling this function.
func splitPoint(n int) int {
	if n <= 2 {
		return 1
	}
	k := 1
	for k<<1 < n {
		k <<= 1
	}
	return k
}

// MerkleRoot computes the root of raw leaf payloads.
func MerkleRoot(leaves [][]byte) []byte { return NewTree(leaves).Root() }

// InclusionProof computes an inclusion path for raw leaf payloads.
func InclusionProof(leaves [][]byte, index uint64) ([][]byte, error) {
	return NewTree(leaves).InclusionProof(index)
}

// ConsistencyProof computes a consistency proof for raw leaf payloads.
func ConsistencyProof(leaves [][]byte, oldSize, newSize uint64) ([][]byte, error) {
	return NewTree(leaves).ConsistencyProof(oldSize, newSize)
}

// VerifyInclusion verifies a raw module leaf against a tree checkpoint.
func VerifyInclusion(leaf []byte, index, treeSize uint64, proof [][]byte, root []byte) bool {
	return VerifyInclusionHash(HashLeaf(leaf), index, treeSize, proof, root)
}

// VerifyInclusionProof is an alias for VerifyInclusion.
func VerifyInclusionProof(leaf []byte, index, treeSize uint64, proof [][]byte, root []byte) bool {
	return VerifyInclusion(leaf, index, treeSize, proof, root)
}

// CheckInclusionProof is a proof-first convenience form.
func CheckInclusionProof(proof [][]byte, leaf []byte, index, treeSize uint64, root []byte) bool {
	return VerifyInclusion(leaf, index, treeSize, proof, root)
}

// VerifyInclusionHash verifies an already-hashed leaf.
func VerifyInclusionHash(leafHash []byte, index, treeSize uint64, proof [][]byte, root []byte) bool {
	if len(leafHash) != HashSize || len(root) != HashSize || treeSize == 0 || index >= treeSize {
		return false
	}

	fn, sn := index, treeSize-1
	r := cloneBytes(leafHash)
	for _, p := range proof {
		if sn == 0 || len(p) != HashSize {
			return false
		}
		if fn&1 == 1 || fn == sn {
			r = HashNode(p, r)
			for fn&1 == 0 && fn != 0 {
				fn >>= 1
				sn >>= 1
			}
		} else {
			r = HashNode(r, p)
		}
		fn >>= 1
		sn >>= 1
	}
	return sn == 0 && equalHash(root, r)
}

// VerifyConsistency verifies a consistency proof between two checkpoints.
func VerifyConsistency(oldSize, newSize uint64, oldRoot, newRoot []byte, proof [][]byte) bool {
	if len(oldRoot) != HashSize || len(newRoot) != HashSize {
		return false
	}
	if oldSize > newSize {
		return false
	}
	if oldSize == 0 {
		return bytes.Equal(oldRoot, EmptyRoot()) && len(proof) == 0
	}
	if oldSize == newSize {
		return len(proof) == 0 && bytes.Equal(oldRoot, newRoot)
	}
	if len(proof) == 0 {
		return false
	}

	// Accept both the canonical proof form and the older "old root prefixed"
	// form. The latter is a safe shortening rule: the prefix is redundant
	// because the verifier already knows oldRoot.
	if verifyConsistencyCanonical(oldSize, newSize, oldRoot, newRoot, proof) {
		return true
	}
	if len(proof) > 1 && equalHash(proof[0], oldRoot) &&
		verifyConsistencyCanonical(oldSize, newSize, oldRoot, newRoot, proof[1:]) {
		return true
	}
	return false
}

// VerifyConsistencyProof is an alias for VerifyConsistency.
func VerifyConsistencyProof(oldSize, newSize uint64, oldRoot, newRoot []byte, proof [][]byte) bool {
	return VerifyConsistency(oldSize, newSize, oldRoot, newRoot, proof)
}

// CheckConsistencyProof is a proof-first convenience form.
func CheckConsistencyProof(proof [][]byte, oldSize, newSize uint64, oldRoot, newRoot []byte) bool {
	return VerifyConsistency(oldSize, newSize, oldRoot, newRoot, proof)
}

// VerifyConsistencyCheckpoints verifies a consistency proof from one
// checkpoint to another.
func VerifyConsistencyCheckpoints(from, to Checkpoint, proof [][]byte) bool {
	return VerifyConsistency(from.Size, to.Size, from.Root, to.Root, proof)
}

func verifyConsistencyCanonical(oldSize, newSize uint64, oldRoot, newRoot []byte, proof [][]byte) bool {
	fn, sn := oldSize-1, newSize-1
	for fn&1 == 1 {
		fn >>= 1
		sn >>= 1
	}

	var fr, sr []byte
	idx := 0
	if fn == 0 {
		// The old tree is a complete prefix subtree. The verifier starts
		// from the already-trusted old root.
		fr, sr = cloneBytes(oldRoot), cloneBytes(oldRoot)
	} else {
		if len(proof) == 0 || len(proof[0]) != HashSize {
			return false
		}
		fr, sr = cloneBytes(proof[0]), cloneBytes(proof[0])
		idx = 1
	}

	for ; idx < len(proof); idx++ {
		if sn == 0 || len(proof[idx]) != HashSize {
			return false
		}
		node := proof[idx]
		if fn&1 == 1 || fn == sn {
			fr = HashNode(node, fr)
			sr = HashNode(node, sr)
			for fn&1 == 0 && fn != 0 {
				fn >>= 1
				sn >>= 1
			}
		} else {
			sr = HashNode(sr, node)
		}
		fn >>= 1
		sn >>= 1
	}
	return sn == 0 && equalHash(oldRoot, fr) && equalHash(newRoot, sr)
}

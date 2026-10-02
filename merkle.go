// Package merklemulti implements a Merkle tree together with a multi-proof
// compressor that merges independent inclusion proofs for many leaves into a
// single deduplicated DAG proof.
package merklemulti

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
)

// Hash is a 32-byte SHA-256 digest used for every node in the tree.
type Hash = [sha256.Size]byte

// Tree is a complete binary Merkle tree. The leaf count is padded up to the
// next power of two with zero hashes so every level is full.
type Tree struct {
	// levels[0] holds the leaves, levels[len-1] holds the single root.
	levels   [][]Hash
	numLeafs int // number of real (non-padding) leaves
}

// nodeKey identifies a node inside the tree by (level, index).
type nodeKey struct {
	level int
	index int
}

func hashLeaf(data []byte) Hash {
	return sha256.Sum256(append([]byte{0x00}, data...))
}

func hashNode(left, right Hash) Hash {
	buf := make([]byte, 0, 1+2*sha256.Size)
	buf = append(buf, 0x01)
	buf = append(buf, left[:]...)
	buf = append(buf, right[:]...)
	return sha256.Sum256(buf)
}

// NewTree builds a Merkle tree from the given leaf payloads.
func NewTree(leaves [][]byte) *Tree {
	if len(leaves) == 0 {
		panic("merklemulti: tree requires at least one leaf")
	}
	n := 1
	for n < len(leaves) {
		n <<= 1
	}
	level := make([]Hash, n)
	for i := range level {
		if i < len(leaves) {
			level[i] = hashLeaf(leaves[i])
		}
	}
	t := &Tree{levels: [][]Hash{level}, numLeafs: len(leaves)}
	for len(level) > 1 {
		next := make([]Hash, len(level)/2)
		for i := range next {
			next[i] = hashNode(level[2*i], level[2*i+1])
		}
		t.levels = append(t.levels, next)
		level = next
	}
	return t
}

// Root returns the Merkle root of the tree.
func (t *Tree) Root() Hash {
	return t.levels[len(t.levels)-1][0]
}

// Depth returns the number of hashing rounds between leaves and root.
func (t *Tree) Depth() int {
	return len(t.levels) - 1
}

// NumLeaves returns the padded leaf count (a power of two).
func (t *Tree) NumLeaves() int {
	return len(t.levels[0])
}

// LeafHash returns the hash of the padded leaf at index i.
func (t *Tree) LeafHash(i int) Hash {
	return t.levels[0][i]
}

// Proof is an independent inclusion proof for a single leaf: one sibling
// hash per level, bottom-up.
type Proof struct {
	LeafIndex int
	LeafHash  Hash
	Siblings  []Hash
}

// Prove generates the independent inclusion proof for leaf index i.
func (t *Tree) Prove(i int) (*Proof, error) {
	if i < 0 || i >= t.NumLeaves() {
		return nil, fmt.Errorf("merklemulti: leaf index %d out of range", i)
	}
	p := &Proof{LeafIndex: i, LeafHash: t.levels[0][i]}
	idx := i
	for level := 0; level < t.Depth(); level++ {
		p.Siblings = append(p.Siblings, t.levels[level][idx^1])
		idx >>= 1
	}
	return p, nil
}

// VerifyProof checks an independent proof against an expected root.
func VerifyProof(root Hash, p *Proof) bool {
	h := p.LeafHash
	idx := p.LeafIndex
	for _, sib := range p.Siblings {
		if idx&1 == 0 {
			h = hashNode(h, sib)
		} else {
			h = hashNode(sib, h)
		}
		idx >>= 1
	}
	return h == root
}

// MultiProof is the compressed batch proof: every shared ancestor hash is
// stored exactly once. Hashes are ordered by level (leaves upward) and by
// index within a level, so verification consumes them in a single pass.
type MultiProof struct {
	NumLeaves   int    // padded leaf count of the tree
	LeafIndices []int  // sorted, distinct proven leaf indices
	LeafHashes  []Hash // hashes matching LeafIndices
	ProofHashes []Hash // deduplicated sibling hashes, level-major order
}

// Size returns the number of 32-byte hashes carried by the proof.
func (mp *MultiProof) Size() int {
	return len(mp.LeafHashes) + len(mp.ProofHashes)
}

// ProveMulti compresses the independent proofs for the given leaf indices
// into a single MultiProof. Ancestor hashes shared by several paths are
// emitted only once, and hashes that can be recomputed on the verifier side
// are omitted entirely.
func (t *Tree) ProveMulti(indices []int) (*MultiProof, error) {
	if len(indices) == 0 {
		return nil, errors.New("merklemulti: no leaf indices given")
	}
	sorted := append([]int(nil), indices...)
	sort.Ints(sorted)
	for i, idx := range sorted {
		if idx < 0 || idx >= t.NumLeaves() {
			return nil, fmt.Errorf("merklemulti: leaf index %d out of range", idx)
		}
		if i > 0 && sorted[i-1] == idx {
			return nil, fmt.Errorf("merklemulti: duplicate leaf index %d", idx)
		}
	}

	mp := &MultiProof{NumLeaves: t.NumLeaves(), LeafIndices: sorted}
	// known holds the nodes whose hash the verifier can compute or was given.
	known := make(map[nodeKey]bool)
	for _, idx := range sorted {
		known[nodeKey{0, idx}] = true
		mp.LeafHashes = append(mp.LeafHashes, t.levels[0][idx])
	}
	for level := 0; level < t.Depth(); level++ {
		next := make(map[nodeKey]bool)
		// Deterministic emission order: ascending node index per level,
		// matching the consumption order in VerifyMulti.
		var keys []nodeKey
		for key := range known {
			if key.level == level {
				keys = append(keys, key)
			}
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i].index < keys[j].index })
		for _, key := range keys {
			sib := nodeKey{level, key.index ^ 1}
			if !known[sib] {
				// The sibling cannot be recomputed: include its hash once.
				mp.ProofHashes = append(mp.ProofHashes, t.levels[level][sib.index])
				known[sib] = true
			}
			next[nodeKey{level + 1, key.index >> 1}] = true
		}
		for key := range next {
			known[key] = true
		}
	}
	return mp, nil
}

// VerifyMulti replays a MultiProof in one bottom-up traversal and compares
// the recomputed root with the expected one.
func VerifyMulti(root Hash, mp *MultiProof) bool {
	if len(mp.LeafIndices) == 0 || len(mp.LeafIndices) != len(mp.LeafHashes) {
		return false
	}
	if mp.NumLeaves <= 0 || mp.NumLeaves&(mp.NumLeaves-1) != 0 {
		return false
	}
	depth := 0
	for n := mp.NumLeaves; n > 1; n >>= 1 {
		depth++
	}

	// current maps node index -> hash for the level being processed.
	current := make(map[int]Hash, len(mp.LeafIndices))
	for i, idx := range mp.LeafIndices {
		if idx < 0 || idx >= mp.NumLeaves {
			return false
		}
		if i > 0 && mp.LeafIndices[i-1] >= idx {
			return false // must be sorted and distinct
		}
		current[idx] = mp.LeafHashes[i]
	}

	proofPos := 0
	for level := 0; level < depth; level++ {
		next := make(map[int]Hash)
		// Deterministic order: iterate indices ascending, matching the order
		// in which ProveMulti emitted proof hashes.
		keys := make([]int, 0, len(current))
		for idx := range current {
			keys = append(keys, idx)
		}
		sort.Ints(keys)
		for _, idx := range keys {
			h, ok := current[idx]
			if !ok {
				continue
			}
			sibIdx := idx ^ 1
			sib, ok := current[sibIdx]
			if !ok {
				if proofPos >= len(mp.ProofHashes) {
					return false
				}
				sib = mp.ProofHashes[proofPos]
				proofPos++
				current[sibIdx] = sib
			}
			var parent Hash
			if idx&1 == 0 {
				parent = hashNode(h, sib)
			} else {
				parent = hashNode(sib, h)
			}
			next[idx>>1] = parent
		}
		current = next
	}
	if proofPos != len(mp.ProofHashes) {
		return false // unconsumed proof hashes
	}
	rootHash, ok := current[0]
	return ok && len(current) == 1 && rootHash == root
}

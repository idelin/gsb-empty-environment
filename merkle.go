// Package multiproof implements a Merkle multi-proof compressor.
//
// Independent inclusion proofs for several leaves share many ancestor
// hashes. This package merges those paths into a single deduplicated DAG
// proof: every hash needed by the verifier is transmitted exactly once,
// and the verifier recomputes the root in one sequential pass.
package multiproof

import (
	"crypto/sha256"
	"errors"
	"sort"
)

// Hash is a 32-byte SHA-256 digest.
type Hash [32]byte

func hashPair(left, right Hash) Hash {
	var buf [64]byte
	copy(buf[:32], left[:])
	copy(buf[32:], right[:])
	return sha256.Sum256(buf[:])
}

// HashLeaf hashes raw leaf data into a leaf node.
func HashLeaf(data []byte) Hash {
	return sha256.Sum256(data)
}

// Tree is a binary Merkle tree. Odd levels are padded by duplicating the
// last node, so every level has an even number of nodes except the root.
type Tree struct {
	levels [][]Hash // levels[0] = leaf hashes, levels[depth] = root
}

// NewTree builds a Merkle tree from raw leaf data.
func NewTree(leaves [][]byte) *Tree {
	if len(leaves) == 0 {
		panic("multiproof: tree needs at least one leaf")
	}
	level := make([]Hash, len(leaves))
	for i, data := range leaves {
		level[i] = HashLeaf(data)
	}
	t := &Tree{levels: [][]Hash{level}}
	for len(level) > 1 {
		if len(level)%2 == 1 {
			level = append(append([]Hash{}, level...), level[len(level)-1])
		}
		t.levels[len(t.levels)-1] = level
		next := make([]Hash, len(level)/2)
		for i := range next {
			next[i] = hashPair(level[2*i], level[2*i+1])
		}
		t.levels = append(t.levels, next)
		level = next
	}
	return t
}

// Depth returns the number of edges between leaves and root.
func (t *Tree) Depth() int { return len(t.levels) - 1 }

// LeafCount returns the number of original (unpadded) leaves.
func (t *Tree) LeafCount() int { return len(t.levels[0]) }

// Root returns the Merkle root.
func (t *Tree) Root() Hash { return t.levels[len(t.levels)-1][0] }

// Proof is an independent single-leaf inclusion proof.
type Proof struct {
	Index    int
	Leaf     Hash
	Siblings []Hash
}

// GenerateProof produces an independent proof for one leaf.
func (t *Tree) GenerateProof(index int) (*Proof, error) {
	if index < 0 || index >= t.LeafCount() {
		return nil, errors.New("multiproof: leaf index out of range")
	}
	p := &Proof{Index: index, Leaf: t.levels[0][index]}
	i := index
	for level := 0; level < t.Depth(); level++ {
		sib := i ^ 1
		p.Siblings = append(p.Siblings, t.levels[level][sib])
		i /= 2
	}
	return p, nil
}

// VerifyProof checks an independent single-leaf proof against a root.
func VerifyProof(root Hash, p *Proof) bool {
	h := p.Leaf
	i := p.Index
	for _, sib := range p.Siblings {
		if i%2 == 0 {
			h = hashPair(h, sib)
		} else {
			h = hashPair(sib, h)
		}
		i /= 2
	}
	return h == root
}

// MultiProof is a compressed batch proof for several distinct leaves.
// ProofHashes holds every deduplicated sibling hash, ordered bottom-up by
// level and left-to-right within a level, so the verifier consumes it in a
// single sequential pass.
type MultiProof struct {
	Indices     []int  // sorted leaf indices being proven
	LeafHashes  []Hash // leaf hashes, aligned with Indices
	ProofHashes []Hash // deduplicated DAG nodes
	Depth       int
}

// Size returns the number of hashes transmitted in the proof.
func (m *MultiProof) Size() int { return len(m.ProofHashes) }

// GenerateMultiProof merges the independent proof paths of the given leaf
// indices into one DAG proof, emitting each shared ancestor hash only once.
func (t *Tree) GenerateMultiProof(indices []int) (*MultiProof, error) {
	if len(indices) == 0 {
		return nil, errors.New("multiproof: no leaf indices given")
	}
	sorted := append([]int{}, indices...)
	sort.Ints(sorted)
	for i, idx := range sorted {
		if idx < 0 || idx >= t.LeafCount() {
			return nil, errors.New("multiproof: leaf index out of range")
		}
		if i > 0 && sorted[i-1] == idx {
			return nil, errors.New("multiproof: duplicate leaf index")
		}
	}

	mp := &MultiProof{Indices: sorted, Depth: t.Depth()}
	for _, idx := range sorted {
		mp.LeafHashes = append(mp.LeafHashes, t.levels[0][idx])
	}

	// known = set of node indices at the current level whose hashes the
	// verifier can already compute. Anything else required on a path is a
	// sibling that must be shipped in the proof — exactly once.
	known := make(map[int]bool, len(sorted))
	for _, idx := range sorted {
		known[idx] = true
	}
	for level := 0; level < t.Depth(); level++ {
		// Process nodes in sorted order so proof hashes are emitted
		// deterministically (bottom-up, left-to-right).
		order := make([]int, 0, len(known))
		for idx := range known {
			order = append(order, idx)
		}
		sort.Ints(order)
		next := make(map[int]bool)
		for _, idx := range order {
			sib := idx ^ 1
			if !known[sib] {
				mp.ProofHashes = append(mp.ProofHashes, t.levels[level][sib])
			}
			next[idx/2] = true
		}
		known = next
	}
	return mp, nil
}

// VerifyMultiProof recomputes the root from a MultiProof in one pass over
// ProofHashes and compares it with the expected root.
func VerifyMultiProof(root Hash, mp *MultiProof) bool {
	if len(mp.Indices) == 0 || len(mp.Indices) != len(mp.LeafHashes) {
		return false
	}
	known := make(map[int]Hash, len(mp.Indices))
	for i, idx := range mp.Indices {
		if idx < 0 || (i > 0 && mp.Indices[i-1] >= idx) {
			return false // indices must be sorted, unique, non-negative
		}
		known[idx] = mp.LeafHashes[i]
	}
	cursor := 0
	for level := 0; level < mp.Depth; level++ {
		order := make([]int, 0, len(known))
		for idx := range known {
			order = append(order, idx)
		}
		sort.Ints(order)
		next := make(map[int]Hash)
		for _, idx := range order {
			h := known[idx]
			sib := idx ^ 1
			sibHash, ok := known[sib]
			if !ok {
				if cursor >= len(mp.ProofHashes) {
					return false
				}
				sibHash = mp.ProofHashes[cursor]
				cursor++
			}
			if idx%2 == 0 {
				next[idx/2] = hashPair(h, sibHash)
			} else {
				next[idx/2] = hashPair(sibHash, h)
			}
		}
		known = next
	}
	if cursor != len(mp.ProofHashes) || len(known) != 1 {
		return false
	}
	computed, ok := known[0]
	return ok && computed == root
}

// IndependentProofSize returns the total number of sibling hashes that
// independent single-leaf proofs for the given indices would carry:
// one hash per tree level per leaf.
func (t *Tree) IndependentProofSize(indices []int) int {
	return len(indices) * t.Depth()
}

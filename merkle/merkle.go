// Package merkle implements a dynamic Merkle tree over ordered leaves,
// with inclusion proofs, using only the Go standard library.
//
// Hash scheme (domain-separated, SHA-256):
//
//	leaf     = SHA256(0x00 || data)
//	internal = SHA256(0x01 || left || right)
//
// Odd nodes at a level are promoted (carried up) unchanged.
package merkle

import (
	"crypto/sha256"
	"errors"
	"fmt"
)

// LeafHash hashes raw leaf data with the leaf domain separator.
func LeafHash(data []byte) []byte {
	h := sha256.New()
	h.Write([]byte{0x00})
	h.Write(data)
	return h.Sum(nil)
}

// nodeHash hashes two child hashes with the internal-node domain separator.
func nodeHash(left, right []byte) []byte {
	h := sha256.New()
	h.Write([]byte{0x01})
	h.Write(left)
	h.Write(right)
	return h.Sum(nil)
}

// Tree is a Merkle tree built over an ordered list of leaf payloads.
// It is "dynamic" in the sense that callers may mutate the leaf list and
// rebuild; Rebuild recomputes only the tree layers.
type Tree struct {
	leaves [][]byte   // raw leaf payloads
	layers [][][]byte // layers[0] = leaf hashes, layers[last] = root
}

// NewTree builds a Merkle tree from the given ordered leaf payloads.
func NewTree(leaves [][]byte) *Tree {
	t := &Tree{leaves: leaves}
	t.Rebuild()
	return t
}

// Leaves returns the raw leaf payloads (shared slice; do not mutate).
func (t *Tree) Leaves() [][]byte { return t.leaves }

// Rebuild recomputes all tree layers from the current leaf payloads.
func (t *Tree) Rebuild() {
	if len(t.leaves) == 0 {
		t.layers = nil
		return
	}
	level := make([][]byte, len(t.leaves))
	for i, data := range t.leaves {
		level[i] = LeafHash(data)
	}
	t.layers = [][][]byte{level}
	for len(level) > 1 {
		next := make([][]byte, 0, (len(level)+1)/2)
		for i := 0; i < len(level); i += 2 {
			if i+1 < len(level) {
				next = append(next, nodeHash(level[i], level[i+1]))
			} else {
				next = append(next, level[i]) // promote odd node
			}
		}
		level = next
		t.layers = append(t.layers, level)
	}
}

// Root returns the Merkle root, or nil for an empty tree.
func (t *Tree) Root() []byte {
	if len(t.layers) == 0 {
		return nil
	}
	top := t.layers[len(t.layers)-1]
	return top[0]
}

// Step is one sibling hop in an inclusion proof.
type Step struct {
	Hash  []byte // sibling hash
	Right bool   // true if the sibling is on the right of the running node
}

// Proof is an inclusion proof for a single leaf.
type Proof struct {
	Index uint64 // leaf index in the tree
	Size  uint64 // number of leaves in the tree
	Steps []Step // sibling hashes from leaf level up to the root
}

// Prove generates an inclusion proof for the leaf at index.
func (t *Tree) Prove(index int) (*Proof, error) {
	if len(t.layers) == 0 {
		return nil, errors.New("merkle: cannot prove in empty tree")
	}
	if index < 0 || index >= len(t.leaves) {
		return nil, fmt.Errorf("merkle: index %d out of range [0,%d)", index, len(t.leaves))
	}
	p := &Proof{Index: uint64(index), Size: uint64(len(t.leaves))}
	idx := index
	for level := 0; level < len(t.layers)-1; level++ {
		nodes := t.layers[level]
		if idx%2 == 0 { // sibling on the right (or promoted: no sibling)
			if idx+1 < len(nodes) {
				p.Steps = append(p.Steps, Step{Hash: nodes[idx+1], Right: true})
			}
		} else { // sibling on the left
			p.Steps = append(p.Steps, Step{Hash: nodes[idx-1], Right: false})
		}
		idx /= 2
	}
	return p, nil
}

// ComputeRoot folds a leaf hash up through the proof steps and returns the
// candidate root. It is exported so combiners can chain proofs: the output
// root of one proof becomes the leaf of the next.
func ComputeRoot(leafHash []byte, p *Proof) []byte {
	h := leafHash
	for _, s := range p.Steps {
		if s.Right {
			h = nodeHash(h, s.Hash)
		} else {
			h = nodeHash(s.Hash, h)
		}
	}
	return h
}

// VerifyProof checks that leafHash, folded through p, equals root.
func VerifyProof(root, leafHash []byte, p *Proof) bool {
	if p == nil || root == nil {
		return false
	}
	return bytesEqual(ComputeRoot(leafHash, p), root)
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := range a {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

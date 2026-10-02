// Package erasable implements an erasable compliance data redaction prover.
//
// Records are committed as leaves of a Merkle tree. When a user exercises
// the "right to be forgotten", the corresponding leaf is rewritten in place
// to an Erase_Marker hash carrying a deletion signature. The tree root
// evolves smoothly and inclusion proofs for all remaining records stay
// valid, while the erased plaintext is physically zeroed out.
package erasable

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
)

// Domain separation prefixes for hashing.
const (
	prefixLeaf   = 0x00
	prefixNode   = 0x01
	prefixMarker = 0xE1
)

// ErrIndexOutOfRange is returned when a leaf index is invalid.
var ErrIndexOutOfRange = errors.New("erasable: leaf index out of range")

// HashSize is the size in bytes of every hash in the tree.
const HashSize = sha256.Size

// LeafHash computes the committed hash of a record payload.
func LeafHash(data []byte) []byte {
	h := sha256.New()
	h.Write([]byte{prefixLeaf})
	h.Write(data)
	return h.Sum(nil)
}

// nodeHash computes the hash of an internal tree node.
func nodeHash(left, right []byte) []byte {
	h := sha256.New()
	h.Write([]byte{prefixNode})
	h.Write(left)
	h.Write(right)
	return h.Sum(nil)
}

// Tree is an append-oriented Merkle tree whose leaves may be rewritten
// in place (used to substitute Erase_Marker hashes upon deletion).
type Tree struct {
	leaves [][]byte
}

// NewTree returns an empty Merkle tree.
func NewTree() *Tree { return &Tree{} }

// Len returns the number of leaves.
func (t *Tree) Len() int { return len(t.leaves) }

// Append adds a leaf hash and returns its index.
func (t *Tree) Append(leaf []byte) int {
	cp := make([]byte, len(leaf))
	copy(cp, leaf)
	t.leaves = append(t.leaves, cp)
	return len(t.leaves) - 1
}

// Leaf returns a copy of the leaf hash at index i.
func (t *Tree) Leaf(i int) ([]byte, error) {
	if i < 0 || i >= len(t.leaves) {
		return nil, ErrIndexOutOfRange
	}
	cp := make([]byte, HashSize)
	copy(cp, t.leaves[i])
	return cp, nil
}

// SetLeaf rewrites the leaf at index i in place. Indices of all other
// leaves are unchanged, so their proofs remain structurally valid.
func (t *Tree) SetLeaf(i int, leaf []byte) error {
	if i < 0 || i >= len(t.leaves) {
		return ErrIndexOutOfRange
	}
	cp := make([]byte, len(leaf))
	copy(cp, leaf)
	t.leaves[i] = cp
	return nil
}

// Root returns the current Merkle root. The empty tree hashes to the
// hash of a single zero domain byte, keeping the root well defined.
func (t *Tree) Root() []byte {
	if len(t.leaves) == 0 {
		h := sha256.Sum256([]byte{prefixLeaf})
		return h[:]
	}
	level := make([][]byte, len(t.leaves))
	for i, l := range t.leaves {
		level[i] = l
	}
	for len(level) > 1 {
		next := make([][]byte, 0, (len(level)+1)/2)
		for i := 0; i < len(level); i += 2 {
			if i+1 < len(level) {
				next = append(next, nodeHash(level[i], level[i+1]))
			} else {
				// Odd node is promoted to the next level unchanged.
				next = append(next, level[i])
			}
		}
		level = next
	}
	out := make([]byte, HashSize)
	copy(out, level[0])
	return out
}

// Proof is a Merkle inclusion proof: one entry per tree level between
// the leaf and the root, bottom-up. A nil entry marks a level where the
// node was the odd one out and was promoted without a sibling.
type Proof struct {
	Index    int
	Siblings [][]byte
}

// Prove returns the inclusion proof for the leaf at index i.
func (t *Tree) Prove(i int) (*Proof, error) {
	if i < 0 || i >= len(t.leaves) {
		return nil, ErrIndexOutOfRange
	}
	level := make([][]byte, len(t.leaves))
	copy(level, t.leaves)
	p := &Proof{Index: i}
	idx := i
	for len(level) > 1 {
		var sib int
		if idx%2 == 0 {
			sib = idx + 1
		} else {
			sib = idx - 1
		}
		if sib < len(level) {
			s := make([]byte, HashSize)
			copy(s, level[sib])
			p.Siblings = append(p.Siblings, s)
		} else {
			// Odd node promoted to the next level: record a nil
			// placeholder so verifiers stay aligned per level.
			p.Siblings = append(p.Siblings, nil)
		}
		// Build next level (odd nodes promoted).
		next := make([][]byte, 0, (len(level)+1)/2)
		for j := 0; j < len(level); j += 2 {
			if j+1 < len(level) {
				next = append(next, nodeHash(level[j], level[j+1]))
			} else {
				next = append(next, level[j])
			}
		}
		level = next
		idx /= 2
	}
	return p, nil
}

// Verify checks that leaf is committed at p.Index under root.
func (p *Proof) Verify(leaf, root []byte) bool {
	cur := make([]byte, len(leaf))
	copy(cur, leaf)
	idx := p.Index
	for _, sib := range p.Siblings {
		if sib == nil {
			// Promoted level: hash passes through unchanged.
			idx /= 2
			continue
		}
		if idx%2 == 0 {
			cur = nodeHash(cur, sib)
		} else {
			cur = nodeHash(sib, cur)
		}
		idx /= 2
	}
	if len(cur) != len(root) {
		return false
	}
	for i := range cur {
		if cur[i] != root[i] {
			return false
		}
	}
	return true
}

// encodeUint64 returns the big-endian encoding of v, used to make
// marker hashes unambiguous.
func encodeUint64(v uint64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	return b[:]
}

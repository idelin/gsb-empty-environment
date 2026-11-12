package netsec

import (
	"crypto/sha256"
	"encoding/binary"
)

// Proof authenticates one leaf against a root digest. Levels are ordered
// leaf-first: Siblings[0] are the leaf's siblings, Siblings[last] are the
// root's children. Position[i] is where the running digest sits among its
// level's children, so proofs are self-contained and need no fanout.
type Proof struct {
	Siblings [][]Digest
	Position []int
}

// Snapshot is a pinned root held by a reader. It keeps authenticating its
// version of the tree regardless of later commits.
type Snapshot struct {
	root      *node
	height    int
	fanout    int
	leafSize  int
	numLeaves int
}

// Root returns the digest this snapshot authenticates against.
func (s Snapshot) Root() Digest {
	return s.root.digest
}

// NumLeaves returns the leaf count of the snapshotted tree.
func (s Snapshot) NumLeaves() int {
	return s.numLeaves
}

// pathTo walks from the root down to leaf index, collecting the proof.
func (s Snapshot) pathTo(index int) (*node, Proof) {
	n := s.root
	var topDown Proof
	for level := s.height; level > 0; level-- {
		span := 1
		for i := 0; i < level-1; i++ {
			span *= s.fanout
		}
		pos := (index / span) % s.fanout
		sibs := make([]Digest, 0, len(n.children)-1)
		for i, c := range n.children {
			if i != pos {
				sibs = append(sibs, c.digest)
			}
		}
		topDown.Siblings = append(topDown.Siblings, sibs)
		topDown.Position = append(topDown.Position, pos)
		n = n.children[pos]
	}
	// Reverse to leaf-first order.
	p := Proof{
		Siblings: make([][]Digest, len(topDown.Siblings)),
		Position: make([]int, len(topDown.Position)),
	}
	for i := range topDown.Siblings {
		p.Siblings[len(topDown.Siblings)-1-i] = topDown.Siblings[i]
		p.Position[len(topDown.Position)-1-i] = topDown.Position[i]
	}
	return n, p
}

// Prove returns the authentication path for a leaf in this snapshot.
func (s Snapshot) Prove(index int) (Proof, error) {
	if index < 0 || index >= s.numLeaves {
		return Proof{}, ErrIndexOutOfRange
	}
	_, p := s.pathTo(index)
	return p, nil
}

// Read returns the leaf bytes only after the authentication path from the
// leaf up to the snapshot root verifies. Bytes are never delivered for a
// path that fails verification.
func (s Snapshot) Read(index int) ([]byte, error) {
	if index < 0 || index >= s.numLeaves {
		return nil, ErrIndexOutOfRange
	}
	leaf, p := s.pathTo(index)
	if !VerifyProof(s.root.digest, leaf.data, p) {
		return nil, ErrIntegrity
	}
	out := make([]byte, len(leaf.data))
	copy(out, leaf.data)
	return out, nil
}

// Prove returns the authentication path for a leaf in the current tree.
func (t *Tree) Prove(index int) (Proof, error) {
	return t.Snapshot().Prove(index)
}

// Read returns the current leaf bytes after verifying the path against the
// current root.
func (t *Tree) Read(index int) ([]byte, error) {
	return t.Snapshot().Read(index)
}

// VerifyProof recomputes the root digest from the leaf bytes and the proof
// and reports whether it matches root.
func VerifyProof(root Digest, leaf []byte, p Proof) bool {
	if len(p.Siblings) != len(p.Position) {
		return false
	}
	d := leafDigest(leaf)
	for level := 0; level < len(p.Siblings); level++ {
		sibs := p.Siblings[level]
		pos := p.Position[level]
		if pos < 0 || pos > len(sibs) {
			return false
		}
		h := sha256.New()
		h.Write([]byte{domainInternal})
		var count [4]byte
		binary.BigEndian.PutUint32(count[:], uint32(len(sibs)+1))
		h.Write(count[:])
		si := 0
		for i := 0; i < len(sibs)+1; i++ {
			if i == pos {
				h.Write(d[:])
			} else {
				h.Write(sibs[si][:])
				si++
			}
		}
		h.Sum(d[:0])
	}
	return d == root
}

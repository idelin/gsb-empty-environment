package erasureproof

import "crypto/sha256"

// Domain-separation prefixes so that leaf hashes and internal-node hashes
// can never collide (second-preimage / proof-confusion resistance).
const (
	leafDomain byte = 0x00
	nodeDomain byte = 0x01
)

// Digest is a SHA-256 hash used for leaves, nodes and roots.
type Digest [sha256.Size]byte

// LeafHash hashes arbitrary leaf payload with the leaf domain prefix.
func LeafHash(payload []byte) Digest {
	return sha256.Sum256(append([]byte{leafDomain}, payload...))
}

// NodeHash combines two child hashes into their parent node hash.
func NodeHash(left, right Digest) Digest {
	buf := make([]byte, 0, 1+2*sha256.Size)
	buf = append(buf, nodeDomain)
	buf = append(buf, left[:]...)
	buf = append(buf, right[:]...)
	return sha256.Sum256(buf)
}

// emptyLeaf is the deterministic padding leaf used to fill the tree up to
// the next power of two. It is publicly derivable, so padding is auditable.
var emptyLeaf = LeafHash([]byte("erasureproof:empty-leaf"))

// Tree is an append-rebuild Merkle tree. It is rebuilt whenever the leaf
// set changes (insertion or erasure); proofs are always generated against
// the current tree.
type Tree struct {
	leaves []Digest
	levels [][]Digest // levels[0] == leaves, levels[last] == {root}
}

// NewTree builds a Merkle tree over leaves, padding with emptyLeaf up to a
// power of two. An empty input yields a single-node tree rooted at emptyLeaf.
func NewTree(leaves []Digest) *Tree {
	n := 1
	for n < len(leaves) {
		n <<= 1
	}
	padded := make([]Digest, n)
	copy(padded, leaves)
	for i := len(leaves); i < n; i++ {
		padded[i] = emptyLeaf
	}
	levels := [][]Digest{padded}
	cur := padded
	for len(cur) > 1 {
		next := make([]Digest, len(cur)/2)
		for i := range next {
			next[i] = NodeHash(cur[2*i], cur[2*i+1])
		}
		levels = append(levels, next)
		cur = next
	}
	return &Tree{leaves: padded, levels: levels}
}

// Root returns the current Merkle root.
func (t *Tree) Root() Digest { return t.levels[len(t.levels)-1][0] }

// NumLeaves returns the padded leaf count.
func (t *Tree) NumLeaves() int { return len(t.leaves) }

// Proof is a Merkle inclusion path: the sibling hashes from leaf to root.
type Proof struct {
	Index    int
	Siblings []Digest
}

// Prove returns the inclusion proof for the leaf at index.
func (t *Tree) Prove(index int) (Proof, error) {
	if index < 0 || index >= len(t.leaves) {
		return Proof{}, ErrIndexOutOfRange
	}
	siblings := make([]Digest, 0, len(t.levels)-1)
	idx := index
	for level := 0; level < len(t.levels)-1; level++ {
		siblings = append(siblings, t.levels[level][idx^1])
		idx >>= 1
	}
	return Proof{Index: index, Siblings: siblings}, nil
}

// Verify recomputes the root from leaf along the proof path and compares it
// with root. The index must be fully consumed by the path so that proofs
// cannot be padded with extra siblings.
func (p Proof) Verify(leaf, root Digest) bool {
	if p.Index < 0 {
		return false
	}
	cur := leaf
	idx := p.Index
	for _, sib := range p.Siblings {
		if idx&1 == 0 {
			cur = NodeHash(cur, sib)
		} else {
			cur = NodeHash(sib, cur)
		}
		idx >>= 1
	}
	return idx == 0 && cur == root
}

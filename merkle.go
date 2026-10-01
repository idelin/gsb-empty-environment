package cdc

import "crypto/sha256"

// MerkleTree is a binary Merkle tree over chunk hashes. Leaves are the
// SHA-256 chunk hashes in stream order; each internal node is the
// SHA-256 of the concatenation of its two children. An odd node at any
// level is paired with itself.
type MerkleTree struct {
	Leaves [][sha256.Size]byte
	levels [][][sha256.Size]byte // levels[0] == Leaves, last level is the root
}

// NewMerkleTree builds a Merkle tree from chunk hashes.
func NewMerkleTree(leaves [][sha256.Size]byte) *MerkleTree {
	t := &MerkleTree{Leaves: leaves}
	if len(leaves) == 0 {
		empty := sha256.Sum256(nil)
		t.levels = [][][sha256.Size]byte{{empty}}
		return t
	}
	level := leaves
	t.levels = append(t.levels, level)
	for len(level) > 1 {
		next := make([][sha256.Size]byte, 0, (len(level)+1)/2)
		for i := 0; i < len(level); i += 2 {
			right := level[i]
			if i+1 < len(level) {
				right = level[i+1]
			}
			var buf [2 * sha256.Size]byte
			copy(buf[:sha256.Size], level[i][:])
			copy(buf[sha256.Size:], right[:])
			next = append(next, sha256.Sum256(buf[:]))
		}
		level = next
		t.levels = append(t.levels, level)
	}
	return t
}

// NewMerkleTreeFromChunks builds a Merkle tree over the given chunks.
func NewMerkleTreeFromChunks(chunks []Chunk) *MerkleTree {
	leaves := make([][sha256.Size]byte, len(chunks))
	for i, c := range chunks {
		leaves[i] = c.Hash
	}
	return NewMerkleTree(leaves)
}

// Root returns the Merkle root hash.
func (t *MerkleTree) Root() [sha256.Size]byte {
	return t.levels[len(t.levels)-1][0]
}

// Depth returns the number of edges from a leaf to the root.
func (t *MerkleTree) Depth() int { return len(t.levels) - 1 }

// ProofNode is one sibling hash on the path from a leaf to the root.
type ProofNode struct {
	Hash    [sha256.Size]byte
	IsRight bool // true if the sibling is the right child
}

// Proof returns the authentication path for leaf index.
func (t *MerkleTree) Proof(index int) []ProofNode {
	if index < 0 || index >= len(t.Leaves) {
		return nil
	}
	var proof []ProofNode
	for level := 0; level < len(t.levels)-1; level++ {
		nodes := t.levels[level]
		sibling := index ^ 1
		if sibling >= len(nodes) {
			sibling = index // odd node pairs with itself
		}
		proof = append(proof, ProofNode{Hash: nodes[sibling], IsRight: sibling > index})
		index /= 2
	}
	return proof
}

// VerifyProof checks that leaf sits at position index in a tree with
// the given root, using the authentication path proof.
func VerifyProof(leaf [sha256.Size]byte, index int, proof []ProofNode, root [sha256.Size]byte) bool {
	h := leaf
	for _, p := range proof {
		var buf [2 * sha256.Size]byte
		if p.IsRight {
			copy(buf[:sha256.Size], h[:])
			copy(buf[sha256.Size:], p.Hash[:])
		} else {
			copy(buf[:sha256.Size], p.Hash[:])
			copy(buf[sha256.Size:], h[:])
		}
		h = sha256.Sum256(buf[:])
		index /= 2
	}
	return h == root
}

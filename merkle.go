package cdc

import "crypto/sha256"

// Tree is a Merkle tree over chunk content hashes. Leaves are the
// per-chunk SHA-256 hashes; every interior node is the SHA-256 of the
// concatenation of its two children. An odd node at any level is
// promoted by duplicating itself.
type Tree struct {
	levels [][][32]byte // levels[0] = leaves, last level = root
}

// BuildTree constructs a Merkle tree over the given leaf hashes.
// It returns nil for an empty input.
func BuildTree(leaves [][32]byte) *Tree {
	if len(leaves) == 0 {
		return nil
	}
	level := make([][32]byte, len(leaves))
	copy(level, leaves)
	t := &Tree{levels: [][][32]byte{level}}
	for len(level) > 1 {
		next := make([][32]byte, 0, (len(level)+1)/2)
		for i := 0; i < len(level); i += 2 {
			right := level[i]
			if i+1 < len(level) {
				right = level[i+1]
			}
			next = append(next, hashPair(level[i], right))
		}
		t.levels = append(t.levels, next)
		level = next
	}
	return t
}

func hashPair(left, right [32]byte) [32]byte {
	var buf [64]byte
	copy(buf[:32], left[:])
	copy(buf[32:], right[:])
	return sha256.Sum256(buf[:])
}

// Root returns the Merkle root, or the zero hash for an empty tree.
func (t *Tree) Root() [32]byte {
	if t == nil {
		return [32]byte{}
	}
	top := t.levels[len(t.levels)-1]
	return top[0]
}

// Leaves returns the leaf hashes (chunk hashes) of the tree.
func (t *Tree) Leaves() [][32]byte {
	if t == nil {
		return nil
	}
	return t.levels[0]
}

// ProofStep is one sibling hash on the path from a leaf to the root.
type ProofStep struct {
	Sibling [32]byte
	Right   bool // true if the sibling is the right child
}

// Proof returns the authentication path for leaf index i.
func (t *Tree) Proof(i int) []ProofStep {
	var proof []ProofStep
	for level := 0; level < len(t.levels)-1; level++ {
		nodes := t.levels[level]
		sib := i ^ 1
		if sib >= len(nodes) {
			sib = i // odd node promoted by duplication
		}
		proof = append(proof, ProofStep{Sibling: nodes[sib], Right: sib > i})
		i /= 2
	}
	return proof
}

// VerifyProof reports whether leaf, combined with proof, reproduces
// root. index is the leaf's position in the tree.
func VerifyProof(leaf [32]byte, proof []ProofStep, root [32]byte) bool {
	h := leaf
	for _, step := range proof {
		if step.Right {
			h = hashPair(h, step.Sibling)
		} else {
			h = hashPair(step.Sibling, h)
		}
	}
	return h == root
}

// Diff compares oldChunks against newChunks by content hash and
// reports which new chunks must be transferred (their hashes do not
// occur in oldChunks) and how many bytes of the new stream are
// already covered by reusable old chunks.
func Diff(oldChunks, newChunks []Chunk) (changed []Chunk, reusedBytes int) {
	old := make(map[[32]byte]int, len(oldChunks))
	for _, ch := range oldChunks {
		old[ch.Hash]++
	}
	for _, ch := range newChunks {
		if old[ch.Hash] > 0 {
			old[ch.Hash]--
			reusedBytes += ch.Length
		} else {
			changed = append(changed, ch)
		}
	}
	return changed, reusedBytes
}

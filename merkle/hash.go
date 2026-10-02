package merkle

import "crypto/sha256"

// Digest is a 32-byte SHA-256 hash used for all nodes in the trees.
type Digest [sha256.Size]byte

// Domain-separation prefixes so leaf hashes and internal node hashes
// can never collide (second-preimage protection).
const (
	leafPrefix = 0x00
	nodePrefix = 0x01
)

// LeafHash hashes a key/value pair into a shard-tree leaf digest.
func LeafHash(key, value []byte) Digest {
	h := sha256.New()
	h.Write([]byte{leafPrefix})
	h.Write(key)
	h.Write(value)
	var d Digest
	copy(d[:], h.Sum(nil))
	return d
}

// nodeHash hashes two child digests into a parent digest.
func nodeHash(left, right Digest) Digest {
	h := sha256.New()
	h.Write([]byte{nodePrefix})
	h.Write(left[:])
	h.Write(right[:])
	var d Digest
	copy(d[:], h.Sum(nil))
	return d
}

// emptyRoot is the canonical root of an empty tree.
var emptyRoot = LeafHash([]byte("empty"), nil)

// buildLevels reduces a slice of leaf digests to a single root by
// repeatedly pairing nodes. An odd trailing node is promoted to the
// next level unchanged. Returns every level (level 0 = leaves) so
// proofs can be extracted without rebuilding.
func buildLevels(leaves []Digest) [][]Digest {
	if len(leaves) == 0 {
		return [][]Digest{{emptyRoot}}
	}
	levels := [][]Digest{leaves}
	current := leaves
	for len(current) > 1 {
		next := make([]Digest, 0, (len(current)+1)/2)
		for i := 0; i < len(current); i += 2 {
			if i+1 < len(current) {
				next = append(next, nodeHash(current[i], current[i+1]))
			} else {
				next = append(next, current[i]) // promote odd node
			}
		}
		levels = append(levels, next)
		current = next
	}
	return levels
}

// ProofStep is one sibling hash on the path from a leaf to the root.
type ProofStep struct {
	Sibling Digest
	// IsLeft is true when the sibling sits to the left of the running hash.
	IsLeft bool
}

// fold replays a proof path from a leaf digest up to the root.
func fold(leaf Digest, path []ProofStep) Digest {
	h := leaf
	for _, step := range path {
		if step.IsLeft {
			h = nodeHash(step.Sibling, h)
		} else {
			h = nodeHash(h, step.Sibling)
		}
	}
	return h
}

// pathFromLevels extracts the sibling path for leaf index idx.
func pathFromLevels(levels [][]Digest, idx int) []ProofStep {
	path := make([]ProofStep, 0, len(levels)-1)
	for level := 0; level < len(levels)-1; level++ {
		nodes := levels[level]
		sibling := idx ^ 1
		if sibling < len(nodes) {
			path = append(path, ProofStep{Sibling: nodes[sibling], IsLeft: sibling < idx})
		}
		// Odd trailing node: promoted without a sibling, no step added.
		idx /= 2
	}
	return path
}

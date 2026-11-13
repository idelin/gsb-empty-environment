package netsec

import (
	"crypto/sha256"
)

var (
	emptyMerkleRoot = sha256.Sum256([]byte("netsec/q0152/merkle-empty/v1"))
	merkleNodeTag   = []byte{0x01}
)

// MerkleProofNode is one sibling on the path from a leaf to the Merkle root.
type MerkleProofNode struct {
	Hash [32]byte
	Left bool
}

// HashCiphertext returns the SHA-256 digest stored for a ciphertext chunk.
func HashCiphertext(ciphertext []byte) [32]byte {
	return sha256.Sum256(ciphertext)
}

// HashMetadata returns the digest bound to every chunk's associated data.
func HashMetadata(metadata []byte) [32]byte {
	return sha256.Sum256(metadata)
}

func hashMerklePair(left, right [32]byte) [32]byte {
	h := sha256.New()
	_, _ = h.Write(merkleNodeTag)
	_, _ = h.Write(left[:])
	_, _ = h.Write(right[:])
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// ComputeMerkleRoot computes the attachment Merkle root over ciphertext
// digests. An odd node at a level is promoted unchanged. The empty tree has a
// fixed, domain-separated root.
func ComputeMerkleRoot(leaves [][32]byte) [32]byte {
	if len(leaves) == 0 {
		return emptyMerkleRoot
	}
	level := append([][32]byte(nil), leaves...)
	for len(level) > 1 {
		next := make([][32]byte, 0, (len(level)+1)/2)
		for i := 0; i+1 < len(level); i += 2 {
			next = append(next, hashMerklePair(level[i], level[i+1]))
		}
		if len(level)%2 == 1 {
			next = append(next, level[len(level)-1])
		}
		level = next
	}
	return level[0]
}

// MerkleProof builds the inclusion proof for one leaf. Proof nodes are ordered
// from the leaf level toward the root.
func MerkleProof(leaves [][32]byte, index uint64) ([]MerkleProofNode, error) {
	if index >= uint64(len(leaves)) {
		return nil, ErrChunkOutOfRange
	}
	if len(leaves) == 0 {
		return nil, ErrChunkOutOfRange
	}

	level := append([][32]byte(nil), leaves...)
	position := index
	proof := make([]MerkleProofNode, 0)
	for len(level) > 1 {
		if levelIsPromoted := len(level)%2 == 1 && position == uint64(len(level)-1); !levelIsPromoted {
			sibling := position ^ 1
			proof = append(proof, MerkleProofNode{
				Hash: level[sibling],
				Left: sibling < position,
			})
		}
		next := make([][32]byte, 0, (len(level)+1)/2)
		for i := 0; i+1 < len(level); i += 2 {
			next = append(next, hashMerklePair(level[i], level[i+1]))
		}
		if len(level)%2 == 1 {
			next = append(next, level[len(level)-1])
		}
		position /= 2
		level = next
	}
	return proof, nil
}

// VerifyMerkleProof verifies a proof against a root and the expected tree
// width. The total leaf count is required so missing or extra proof nodes are
// rejected rather than being silently ignored.
func VerifyMerkleProof(leaf [32]byte, index, total uint64, proof []MerkleProofNode, root [32]byte) bool {
	if total == 0 || index >= total {
		return false
	}
	current := leaf
	position := index
	width := total
	proofIndex := 0
	for width > 1 {
		if !(width%2 == 1 && position == width-1) {
			if proofIndex >= len(proof) {
				return false
			}
			node := proof[proofIndex]
			proofIndex++
			if node.Left {
				current = hashMerklePair(node.Hash, current)
			} else {
				current = hashMerklePair(current, node.Hash)
			}
		}
		position /= 2
		width = (width + 1) / 2
	}
	if proofIndex != len(proof) {
		return false
	}
	return current == root
}

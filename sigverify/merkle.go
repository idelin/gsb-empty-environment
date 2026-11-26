package sigverify

import (
	"crypto/sha256"
)

// RFC 6962 section 2.1 domain-separation prefixes.
const (
	leafHashPrefix byte = 0x00
	nodeHashPrefix byte = 0x01
)

const sha256Size = sha256.Size // 32 bytes

// LeafHash computes the RFC 6962 leaf hash for the canonical, serialized log
// entry: SHA-256(0x00 || canonicalEntryBytes).
func LeafHash(canonicalEntryBytes []byte) []byte {
	h := sha256.New()
	h.Write([]byte{leafHashPrefix})
	h.Write(canonicalEntryBytes)
	return h.Sum(nil)
}

// nodeHash computes the RFC 6962 internal node hash:
// SHA-256(0x01 || left || right). Both inputs must be 32-byte digests.
func nodeHash(left, right []byte) ([]byte, error) {
	if len(left) != sha256Size || len(right) != sha256Size {
		return nil, ErrProofHash
	}
	h := sha256.New()
	h.Write([]byte{nodeHashPrefix})
	h.Write(left)
	h.Write(right)
	return h.Sum(nil), nil
}

// proofSide describes how a single supplied hash is combined while climbing
// the tree.
type proofSide int

const (
	sideLeft  proofSide = iota // supplied hash is the left sibling
	sideRight                  // supplied hash is the right sibling
)

// inclusionSides derives, purely from the leaf index and tree size, whether
// every hash on the RFC 6962 inclusion path is a left or right sibling.
//
// It mirrors the level-by-level RFC 6962 fold: nodes pair up at each level,
// and an odd trailing node is promoted unchanged to the next level. The
// target node index is halved on every level while its neighbor (when present)
// is recorded with the correct side.
func inclusionSides(leafIndex, treeSize uint64) []proofSide {
	var sides []proofSide
	index := leafIndex
	remaining := treeSize
	for remaining > 1 {
		if index%2 == 0 {
			if index+1 < remaining {
				sides = append(sides, sideRight)
			}
			// No right neighbor: the node is promoted without a merge.
		} else {
			sides = append(sides, sideLeft)
		}
		index /= 2
		remaining = (remaining + 1) / 2
	}
	return sides
}

// VerifyInclusion rebuilds the RFC 6962 Merkle tree root from leafHash using
// the supplied inclusion path and confirms it equals expectedRoot.
//
// It rejects illegal indices (leafIndex >= treeSize), non-32-byte hashes, and
// any path whose length does not exactly match the required path (missing or
// extra nodes are both rejected).
func VerifyInclusion(leafIndex, treeSize uint64, leafHash, expectedRoot []byte, hashes [][]byte) error {
	if treeSize == 0 {
		return ErrProofIndex
	}
	if leafIndex >= treeSize {
		return ErrProofIndex
	}
	if len(leafHash) != sha256Size || len(expectedRoot) != sha256Size {
		return ErrProofHash
	}

	sides := inclusionSides(leafIndex, treeSize)
	if len(hashes) != len(sides) {
		return ErrProofLength
	}

	current := make([]byte, sha256Size)
	copy(current, leafHash)

	for idx, sibling := range hashes {
		if len(sibling) != sha256Size {
			return ErrProofHash
		}
		var combined []byte
		var err error
		switch sides[idx] {
		case sideLeft:
			combined, err = nodeHash(sibling, current)
		case sideRight:
			combined, err = nodeHash(current, sibling)
		default:
			return ErrProofIndex
		}
		if err != nil {
			return err
		}
		current = combined
	}

	if !constantTimeEqual(current, expectedRoot) {
		return ErrRootMismatch
	}
	return nil
}

// constantTimeEqual compares two fixed-length hashes without leaking equality
// through timing.
func constantTimeEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

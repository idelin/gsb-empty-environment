package netsec

import (
	"crypto/sha256"
	"errors"
	"fmt"
)

// MaxMerkleLeaves bounds tree size (and therefore proof depth).
const MaxMerkleLeaves uint64 = 1 << 32

// EmptyLeafHash is used to pad a Merkle tree to a power of two.
func EmptyLeafHash() Hash {
	return hashDomainSeparated("merkle-empty-leaf/v1")
}

// EmptyMerkleRoot is the root of a tree with no leaves.
func EmptyMerkleRoot() Hash {
	return hashDomainSeparated("merkle-empty-root/v1")
}

func merkleNode(left, right Hash) Hash {
	return hashDomainSeparated("merkle-node/v1", left[:], right[:])
}

// MerkleProof is an inclusion proof for a leaf at Index in a tree of Total
// leaves. Siblings are ordered from the leaf toward the root.
type MerkleProof struct {
	Index    uint64 `json:"index"`
	Total    uint64 `json:"total"`
	Siblings []Hash `json:"siblings"`
}

func merkleDepth(total uint64) (uint64, uint64, error) {
	if total == 0 {
		return 0, 0, errors.New("netsec: Merkle tree has no leaves")
	}
	if total > MaxMerkleLeaves {
		return 0, 0, fmt.Errorf("netsec: Merkle tree has too many leaves: %d", total)
	}
	var size uint64 = 1
	var depth uint64
	for size < total {
		size <<= 1
		depth++
	}
	return size, depth, nil
}

func buildMerkleLevels(leaves []Hash) ([][]Hash, error) {
	if len(leaves) == 0 {
		return nil, errors.New("netsec: cannot build a tree with no leaves")
	}
	total := uint64(len(leaves))
	size, _, err := merkleDepth(total)
	if err != nil {
		return nil, err
	}
	level := make([]Hash, size)
	copy(level, leaves)
	empty := EmptyLeafHash()
	for i := total; i < size; i++ {
		level[i] = empty
	}
	levels := [][]Hash{level}
	for len(level) > 1 {
		next := make([]Hash, len(level)/2)
		for i := range next {
			next[i] = merkleNode(level[2*i], level[2*i+1])
		}
		levels = append(levels, next)
		level = next
	}
	return levels, nil
}

// BuildMerkleRoot returns the root for leaves. An empty slice maps to
// EmptyMerkleRoot and cannot be used to prove any event.
func BuildMerkleRoot(leaves []Hash) (Hash, error) {
	if len(leaves) == 0 {
		return EmptyMerkleRoot(), nil
	}
	for i, leaf := range leaves {
		if leaf.IsZero() {
			return Hash{}, fmt.Errorf("netsec: Merkle leaf %d is zero", i)
		}
	}
	levels, err := buildMerkleLevels(leaves)
	if err != nil {
		return Hash{}, err
	}
	return levels[len(levels)-1][0], nil
}

// BuildMerkleProof creates an inclusion proof for leaves[index].
func BuildMerkleProof(leaves []Hash, index uint64) (MerkleProof, error) {
	if len(leaves) == 0 {
		return MerkleProof{}, errors.New("netsec: cannot prove a leaf in an empty tree")
	}
	if index >= uint64(len(leaves)) {
		return MerkleProof{}, fmt.Errorf("netsec: leaf index %d out of range", index)
	}
	for i, leaf := range leaves {
		if leaf.IsZero() {
			return MerkleProof{}, fmt.Errorf("netsec: Merkle leaf %d is zero", i)
		}
	}
	levels, err := buildMerkleLevels(leaves)
	if err != nil {
		return MerkleProof{}, err
	}
	proof := MerkleProof{
		Index:    index,
		Total:    uint64(len(leaves)),
		Siblings: make([]Hash, 0, len(levels)-1),
	}
	position := index
	for level := 0; level < len(levels)-1; level++ {
		proof.Siblings = append(proof.Siblings, levels[level][position^1])
		position >>= 1
	}
	return proof, nil
}

// ValidateShape checks bounds without needing the root.
func (p MerkleProof) ValidateShape() error {
	_, depth, err := merkleDepth(p.Total)
	if err != nil {
		return err
	}
	if p.Index >= p.Total {
		return fmt.Errorf("netsec: Merkle index %d out of range %d", p.Index, p.Total)
	}
	if uint64(len(p.Siblings)) != depth {
		return fmt.Errorf("netsec: Merkle proof has %d siblings, need %d", len(p.Siblings), depth)
	}
	for i, sibling := range p.Siblings {
		if sibling.IsZero() {
			return fmt.Errorf("netsec: Merkle sibling %d is zero", i)
		}
	}
	return nil
}

// VerifyMerkleProof verifies that leaf is at proof.Index under root.
func VerifyMerkleProof(root, leaf Hash, proof MerkleProof) error {
	if root.IsZero() {
		return fmt.Errorf("%w: zero root", ErrInvalidMerkleProof)
	}
	if leaf.IsZero() {
		return fmt.Errorf("%w: zero leaf", ErrInvalidMerkleProof)
	}
	if err := proof.ValidateShape(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidMerkleProof, err)
	}
	hash := leaf
	position := proof.Index
	for _, sibling := range proof.Siblings {
		if position&1 == 1 {
			hash = merkleNode(sibling, hash)
		} else {
			hash = merkleNode(hash, sibling)
		}
		position >>= 1
	}
	if position != 0 {
		return fmt.Errorf("%w: index exceeds tree depth", ErrInvalidMerkleProof)
	}
	if hash != root {
		return fmt.Errorf("%w: computed root %s does not match %s", ErrInvalidMerkleProof, hash, root)
	}
	return nil
}

// TxLeafHash commits to a transaction hash for inclusion under Header.TxRoot.
func TxLeafHash(txHash Hash) Hash {
	h := sha256.New()
	writeString(h, hashDomain)
	writeString(h, "tx-leaf/v1")
	writeBytes(h, txHash[:])
	var out Hash
	copy(out[:], h.Sum(nil))
	return out
}

package netsec

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash"
)

const (
	leafHashDomain  = "netsec/q0345/redacted-log/v1/leaf"
	emptyHashDomain = "netsec/q0345/redacted-log/v1/empty"
	nodeHashDomain  = "netsec/q0345/redacted-log/v1/node"
	rootHashDomain  = "netsec/q0345/redacted-log/v1/root"
)

// emptyLeafHash is used only as padding when the number of records is not a
// power of two.  The total record count is bound separately by the log root,
// so padding can never be confused with a real trailing record.
var emptyLeafHash = hashFields(emptyHashDomain)

func hashFields(domain string, fields ...[]byte) []byte {
	h := sha256.New()
	writeField(h, []byte(domain))
	for _, field := range fields {
		writeField(h, field)
	}
	return h.Sum(nil)
}

func writeField(h hash.Hash, value []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = h.Write(length[:])
	_, _ = h.Write(value)
}

func uint64Bytes(value uint64) []byte {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	return encoded[:]
}

func payloadDigest(payload []byte) []byte {
	sum := sha256.Sum256(payload)
	return sum[:]
}

func leafCommitment(logID string, index, sequence uint64, digest []byte) []byte {
	return hashFields(
		leafHashDomain,
		[]byte(logID),
		uint64Bytes(index),
		uint64Bytes(sequence),
		digest,
	)
}

func nodeCommitment(left, right []byte) []byte {
	return hashFields(nodeHashDomain, left, right)
}

func logRoot(logID string, count uint64, merkleRoot []byte) []byte {
	return hashFields(
		rootHashDomain,
		[]byte(logID),
		uint64Bytes(count),
		merkleRoot,
	)
}

type merkleTree struct {
	// levels[0] holds the padded leaves; the last level holds the tree root.
	levels [][][]byte
	size   int
}

func buildMerkleTree(leaves [][]byte) *merkleTree {
	size := 1
	for size < len(leaves) {
		size <<= 1
	}

	level := make([][]byte, size)
	for i := range level {
		level[i] = emptyLeafHash
	}
	copy(level, leaves)

	levels := [][][]byte{level}
	for len(level) > 1 {
		next := make([][]byte, len(level)/2)
		for i := range next {
			next[i] = nodeCommitment(level[2*i], level[2*i+1])
		}
		levels = append(levels, next)
		level = next
	}

	return &merkleTree{levels: levels, size: size}
}

func (tree *merkleTree) root() []byte {
	return tree.levels[len(tree.levels)-1][0]
}

func (tree *merkleTree) membershipPath(index int) []PathStep {
	path := make([]PathStep, 0, len(tree.levels)-1)
	for level := 0; level < len(tree.levels)-1; level++ {
		sibling := index ^ 1
		path = append(path, PathStep{
			Sibling: cloneBytes(tree.levels[level][sibling]),
		})
		index >>= 1
	}
	return path
}

func treeSize(count int) int {
	size := 1
	for size < count {
		size <<= 1
	}
	return size
}

func treeDepth(size int) int {
	depth := 0
	for used := 1; used < size; used <<= 1 {
		depth++
	}
	return depth
}

func rootFromPath(leaf []byte, index int, count uint64, path []PathStep) ([]byte, error) {
	if len(leaf) != sha256.Size {
		return nil, fmt.Errorf("leaf commitment has length %d, want %d", len(leaf), sha256.Size)
	}
	if count == 0 || index < 0 || uint64(index) >= count {
		return nil, fmt.Errorf("position %d outside log of %d records", index, count)
	}

	size := treeSize(int(count))
	if want := treeDepth(size); len(path) != want {
		return nil, fmt.Errorf("membership path has %d steps, want %d", len(path), want)
	}

	current := cloneBytes(leaf)
	for _, step := range path {
		if len(step.Sibling) != sha256.Size {
			return nil, fmt.Errorf(
				"membership sibling has length %d, want %d",
				len(step.Sibling),
				sha256.Size,
			)
		}
		if index&1 == 0 {
			current = nodeCommitment(current, step.Sibling)
		} else {
			current = nodeCommitment(step.Sibling, current)
		}
		index >>= 1
	}
	return current, nil
}

func cloneBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	result := make([]byte, len(value))
	copy(result, value)
	return result
}

// Package snapdelta implements incremental snapshot verification over a
// finite, ordered key space. A receiver that only stores an old root hash
// and a proof can verify that a declared delta (puts, deletes) exactly
// produces the claimed new root: deletions cannot be omitted and
// undeclared blocks cannot be swapped, because every untouched subtree is
// pinned either by a proof node hash or by the canonical empty-subtree
// (default) hash.
package snapdelta

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
)

// Depth is the Merkle tree depth; the key space is [0, 2^Depth).
const Depth = 16

// KeySpaceSize is the number of distinct keys (ordered uint16 IDs).
const KeySpaceSize = 1 << Depth

// Hash is a 32-byte digest.
type Hash [32]byte

// nodeID identifies a tree node by depth (0 = root) and index within its
// level (left-to-right).
type nodeID struct {
	depth uint8
	index uint32
}

// defaultHash[h] is the hash of a completely empty subtree of height h
// (a subtree whose root sits at depth Depth-h). defaultHash[0] is the
// canonical "absent leaf" hash.
var defaultHash [Depth + 1]Hash

func init() {
	for h := 1; h <= Depth; h++ {
		defaultHash[h] = nodeHash(defaultHash[h-1], defaultHash[h-1])
	}
}

// DefaultRoot is the root hash of a completely empty snapshot.
func DefaultRoot() Hash { return defaultHash[Depth] }

func leafHash(key uint16, value []byte) Hash {
	var kb [2]byte
	binary.BigEndian.PutUint16(kb[:], key)
	buf := make([]byte, 0, 3+len(value))
	buf = append(buf, 0x00, kb[0], kb[1])
	buf = append(buf, value...)
	return sha256.Sum256(buf)
}

func nodeHash(left, right Hash) Hash {
	var buf [65]byte
	buf[0] = 0x01
	copy(buf[1:33], left[:])
	copy(buf[33:65], right[:])
	return sha256.Sum256(buf[:])
}

// computeRoot recomputes the tree root from the given leaf hashes
// (touched keys only; absent leaves must be passed as the zero hash) plus
// the inherited-block proof nodes. Every proof node must be consumed as
// the sibling of a computed node (or be the whole-tree root when no leaf
// is touched); unconsumed or overlapping nodes are rejected.
func computeRoot(leaves map[uint16]Hash, nodes map[nodeID]Hash) (Hash, error) {
	nodesByDepth := make([]map[uint32]Hash, Depth+1)
	for id, h := range nodes {
		if nodesByDepth[id.depth] == nil {
			nodesByDepth[id.depth] = make(map[uint32]Hash)
		}
		nodesByDepth[id.depth][id.index] = h
	}
	used := make(map[nodeID]bool, len(nodes))

	cur := make(map[uint32]Hash, len(leaves))
	for k, lh := range leaves {
		cur[uint32(k)] = lh
	}
	for d := Depth; d >= 1; d-- {
		if len(cur) == 0 {
			break
		}
		for idx := range cur {
			if _, ok := nodesByDepth[d][idx]; ok {
				return Hash{}, fmt.Errorf("snapdelta: proof node (depth=%d, index=%d) overlaps a computed node", d, idx)
			}
		}
		next := make(map[uint32]Hash, len(cur))
		for idx, h := range cur {
			parent := idx >> 1
			if _, done := next[parent]; done {
				continue
			}
			sibIdx := idx ^ 1
			sib, ok := cur[sibIdx]
			if !ok {
				id := nodeID{uint8(d), sibIdx}
				if s, found := nodesByDepth[d][sibIdx]; found {
					sib = s
					used[id] = true
				} else {
					sib = defaultHash[Depth-d]
				}
			}
			if idx&1 == 0 {
				next[parent] = nodeHash(h, sib)
			} else {
				next[parent] = nodeHash(sib, h)
			}
		}
		cur = next
	}

	var root Hash
	if len(cur) > 0 {
		root = cur[0]
	} else if h, ok := nodesByDepth[0][0]; ok {
		root = h
		used[nodeID{0, 0}] = true
	} else {
		root = defaultHash[Depth]
	}

	for id := range nodes {
		if !used[id] {
			return Hash{}, fmt.Errorf("snapdelta: unused proof node (depth=%d, index=%d)", id.depth, id.index)
		}
	}
	return root, nil
}

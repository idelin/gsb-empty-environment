package blkidx

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
)

// ChildDesc is the summary of one child (leaf block or internal node) stored
// inside an internal node. It binds the child's content hash, the global
// ordinal range [BaseOff, BaseOff+Count) and the maximum key of the subtree.
type ChildDesc struct {
	Hash    [32]byte
	BaseOff uint64
	Count   uint32
	MaxKey  string
}

// internalNode is a summary node. Its encoding (and therefore its hash)
// commits to the ordered child list, so child order, per-child ranges and
// per-child counts are all authenticated.
type internalNode struct {
	Version  uint32
	NodeID   uint32
	Children []ChildDesc
}

// Internal node encoding:
//
//	"INT1" || version u32 || nodeID u32 || nchildren u16 ||
//	per child: hash [32]byte || baseOff u64 || count u32 || maxKeyLen u16 || maxKey
func encodeInternal(version, nodeID uint32, children []ChildDesc) []byte {
	out := make([]byte, 0, 64)
	out = append(out, 'I', 'N', 'T', '1')
	var tmp [8]byte
	binary.LittleEndian.PutUint32(tmp[:4], version)
	out = append(out, tmp[:4]...)
	binary.LittleEndian.PutUint32(tmp[:4], nodeID)
	out = append(out, tmp[:4]...)
	binary.LittleEndian.PutUint16(tmp[:2], uint16(len(children)))
	out = append(out, tmp[:2]...)
	for _, c := range children {
		out = append(out, c.Hash[:]...)
		binary.LittleEndian.PutUint64(tmp[:8], c.BaseOff)
		out = append(out, tmp[:8]...)
		binary.LittleEndian.PutUint32(tmp[:4], c.Count)
		out = append(out, tmp[:4]...)
		binary.LittleEndian.PutUint16(tmp[:2], uint16(len(c.MaxKey)))
		out = append(out, tmp[:2]...)
		out = append(out, c.MaxKey...)
	}
	return out
}

func internalHash(version, nodeID uint32, children []ChildDesc) [32]byte {
	return sha256.Sum256(encodeInternal(version, nodeID, children))
}

func (n *internalNode) hash() [32]byte {
	return internalHash(n.Version, n.NodeID, n.Children)
}

func sumCounts(children []ChildDesc) uint64 {
	var total uint64
	for _, c := range children {
		total += uint64(c.Count)
	}
	return total
}

// validateChildren checks the intra-node invariants that the verifier
// re-derives: non-empty ordered child list, contiguous offset chaining and
// strictly increasing max keys.
func validateChildren(children []ChildDesc) error {
	if len(children) == 0 {
		return fmt.Errorf("internal node with no children")
	}
	if len(children) > 0xFFFF {
		return fmt.Errorf("too many children: %d", len(children))
	}
	for i, c := range children {
		if c.Count == 0 {
			return fmt.Errorf("child %d has zero count", i)
		}
		if len(c.MaxKey) > 0xFFFF {
			return fmt.Errorf("child %d max key too long", i)
		}
		if i > 0 {
			prev := children[i-1]
			if c.BaseOff != prev.BaseOff+uint64(prev.Count) {
				return fmt.Errorf("child %d base offset %d does not chain from %d+%d",
					i, c.BaseOff, prev.BaseOff, prev.Count)
			}
			if c.MaxKey <= prev.MaxKey {
				return fmt.Errorf("child %d max key %q not greater than previous %q", i, c.MaxKey, prev.MaxKey)
			}
		}
	}
	return nil
}

// routeChild returns the index of the child that must contain key: the first
// child whose MaxKey >= key. If key exceeds every MaxKey it returns the last
// child and false, meaning the key is beyond the subtree's committed range.
func routeChild(children []ChildDesc, key string) (int, bool) {
	for i, c := range children {
		if key <= c.MaxKey {
			return i, true
		}
	}
	return len(children) - 1, false
}

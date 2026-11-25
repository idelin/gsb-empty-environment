package zoneauth

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"sort"
)

// TrieNode is a committed zone-tree node. Children maps label to the hash of
// the committed child node; the wildcard template is simply the child labeled
// "*". Because the whole map is hashed, a committed node authenticates both
// the presence and the absence of every child label.
type TrieNode struct {
	Path     Name              `json:"path"`
	Delegate bool              `json:"delegate,omitempty"`
	Records  []Record          `json:"records,omitempty"`
	Children map[string][]byte `json:"children,omitempty"`
}

// Clone returns a deep copy so snapshots taken before a mutation cannot be
// rewritten by later manager edits.
func (n *TrieNode) Clone() *TrieNode {
	if n == nil {
		return nil
	}
	cp := &TrieNode{
		Path:     n.Path.Clone(),
		Delegate: n.Delegate,
		Records:  append([]Record(nil), n.Records...),
	}
	if len(n.Children) > 0 {
		cp.Children = make(map[string][]byte, len(n.Children))
		for label, hash := range n.Children {
			cp.Children[label] = append([]byte(nil), hash...)
		}
	}
	return cp
}

func putUvarint(dst []byte, x uint64) []byte {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], x)
	return append(dst, buf[:n]...)
}

func putBytes(dst, b []byte) []byte {
	dst = putUvarint(dst, uint64(len(b)))
	return append(dst, b...)
}

func putString(dst []byte, s string) []byte {
	return putBytes(dst, []byte(s))
}

// nodeCanonical renders the deterministic byte image that is hashed. Every
// collection is ordered so the hash is independent of Go map iteration.
func nodeCanonical(n *TrieNode) []byte {
	var buf bytes.Buffer
	buf.WriteString("zoneauth-node/v1\n")

	dst := putUvarint(nil, uint64(len(n.Path)))
	for _, label := range n.Path {
		dst = putString(dst, label)
	}
	buf.Write(dst)

	if n.Delegate {
		buf.WriteByte(1)
	} else {
		buf.WriteByte(0)
	}

	recs := sortedRecords(n.Records)
	dst = putUvarint(nil, uint64(len(recs)))
	for _, rec := range recs {
		dst = putString(dst, rec.Type)
		dst = putString(dst, rec.Value)
	}
	buf.Write(dst)

	labels := make([]string, 0, len(n.Children))
	for label := range n.Children {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	dst = putUvarint(nil, uint64(len(labels)))
	for _, label := range labels {
		dst = putString(dst, label)
		dst = append(dst, n.Children[label]...)
	}
	buf.Write(dst)
	return buf.Bytes()
}

// HashNode computes the committed hash of a trie node.
func HashNode(n *TrieNode) []byte {
	sum := sha256.Sum256(nodeCanonical(n))
	return sum[:]
}

func hashKey(h []byte) string { return hex.EncodeToString(h) }

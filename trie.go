// Package mpt implements a Merkle Patricia Trie: a compact prefix tree
// combined with a Merkle hash tree. Every node carries a content-derived
// hash, so any mutation of any node cascades up and changes the root hash,
// making forged dictionaries detectable.
package mpt

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"sort"
)

// Node is any node in the trie.
type Node interface {
	// Hash returns the SHA-256 digest of the node's canonical encoding,
	// which recursively includes the hashes of all children.
	Hash() []byte
}

// LeafNode stores the remaining (uncompressed) key suffix and a value.
type LeafNode struct {
	Path  []byte // remaining nibbles
	Value []byte
}

// ExtensionNode is a compressed shared path (path compression).
type ExtensionNode struct {
	Path  []byte // shared nibbles
	Child Node
}

// BranchNode is a 16-way radix branch. Value is non-nil when a key
// terminates exactly at this branch.
type BranchNode struct {
	Children [16]Node
	Value    []byte
}

// Trie is a Merkle Patricia Trie.
type Trie struct {
	root Node
}

// NewTrie returns an empty trie.
func NewTrie() *Trie { return &Trie{} }

// RootHash returns the Merkle root of the trie (nil when empty).
func (t *Trie) RootHash() []byte {
	if t.root == nil {
		return nil
	}
	return t.root.Hash()
}

// ---------------------------------------------------------------------------
// hashing
// ---------------------------------------------------------------------------

func writeBytes(h io.Writer, b []byte) {
	var lenBuf [8]byte
	binary.BigEndian.PutUint64(lenBuf[:], uint64(len(b)))
	h.Write(lenBuf[:])
	h.Write(b)
}

func (n *LeafNode) Hash() []byte {
	h := sha256.New()
	h.Write([]byte("leaf"))
	writeBytes(h, n.Path)
	writeBytes(h, n.Value)
	return h.Sum(nil)
}

func (n *ExtensionNode) Hash() []byte {
	h := sha256.New()
	h.Write([]byte("ext"))
	writeBytes(h, n.Path)
	h.Write(n.Child.Hash())
	return h.Sum(nil)
}

func (n *BranchNode) Hash() []byte {
	h := sha256.New()
	h.Write([]byte("branch"))
	for _, c := range n.Children {
		if c == nil {
			h.Write([]byte{0})
		} else {
			h.Write([]byte{1})
			h.Write(c.Hash())
		}
	}
	if n.Value == nil {
		h.Write([]byte{0})
	} else {
		h.Write([]byte{1})
		writeBytes(h, n.Value)
	}
	return h.Sum(nil)
}

// ---------------------------------------------------------------------------
// nibble helpers
// ---------------------------------------------------------------------------

// keyToNibbles expands each byte of the key into two 4-bit nibbles.
func keyToNibbles(key string) []byte {
	n := make([]byte, len(key)*2)
	for i, b := range []byte(key) {
		n[2*i] = b >> 4
		n[2*i+1] = b & 0x0f
	}
	return n
}

func nibblesToKey(n []byte) string {
	b := make([]byte, len(n)/2)
	for i := 0; i+1 < len(n); i += 2 {
		b[i/2] = n[i]<<4 | n[i+1]
	}
	return string(b)
}

func commonPrefixLen(a, b []byte) int {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	return i
}

// ---------------------------------------------------------------------------
// insert
// ---------------------------------------------------------------------------

// Put inserts or updates key with value. Shared paths are compressed into
// extension nodes automatically; the root hash changes on every mutation.
func (t *Trie) Put(key string, value []byte) {
	t.root = insert(t.root, keyToNibbles(key), value)
}

func insert(node Node, key, value []byte) Node {
	switch n := node.(type) {
	case nil:
		return &LeafNode{Path: key, Value: value}

	case *LeafNode:
		cpl := commonPrefixLen(n.Path, key)
		if cpl == len(n.Path) && cpl == len(key) {
			n.Value = value // same key: update in place
			return n
		}
		branch := &BranchNode{}
		if rem := n.Path[cpl:]; len(rem) == 0 {
			branch.Value = n.Value
		} else {
			branch.Children[rem[0]] = &LeafNode{Path: rem[1:], Value: n.Value}
		}
		if rem := key[cpl:]; len(rem) == 0 {
			branch.Value = value
		} else {
			branch.Children[rem[0]] = &LeafNode{Path: rem[1:], Value: value}
		}
		if cpl > 0 {
			return &ExtensionNode{Path: key[:cpl], Child: branch}
		}
		return branch

	case *ExtensionNode:
		cpl := commonPrefixLen(n.Path, key)
		if cpl == len(n.Path) {
			n.Child = insert(n.Child, key[cpl:], value)
			return n
		}
		// split the extension at the divergence point
		branch := &BranchNode{}
		if rem := n.Path[cpl:]; len(rem) == 1 {
			branch.Children[rem[0]] = n.Child
		} else {
			branch.Children[rem[0]] = &ExtensionNode{Path: rem[1:], Child: n.Child}
		}
		if rem := key[cpl:]; len(rem) == 0 {
			branch.Value = value
		} else {
			branch.Children[rem[0]] = &LeafNode{Path: rem[1:], Value: value}
		}
		if cpl > 0 {
			return &ExtensionNode{Path: key[:cpl], Child: branch}
		}
		return branch

	case *BranchNode:
		if len(key) == 0 {
			n.Value = value
			return n
		}
		n.Children[key[0]] = insert(n.Children[key[0]], key[1:], value)
		return n
	}
	panic("unreachable")
}

// ---------------------------------------------------------------------------
// lookup
// ---------------------------------------------------------------------------

// Get returns the value stored under key.
func (t *Trie) Get(key string) ([]byte, bool) {
	node, _, ok := t.find(keyToNibbles(key))
	if !ok {
		return nil, false
	}
	switch n := node.(type) {
	case *LeafNode:
		return n.Value, true
	case *BranchNode:
		return n.Value, true
	}
	return nil, false
}

// find walks the trie following nibbles. It returns the node at which the
// walk terminated. ok is true only if the key is fully consumed and lands
// on a value-carrying node.
func (t *Trie) find(key []byte) (Node, []byte, bool) {
	node := t.root
	rest := key
	for node != nil {
		switch n := node.(type) {
		case *LeafNode:
			if bytes.Equal(n.Path, rest) {
				return n, nil, true
			}
			return n, rest, false
		case *ExtensionNode:
			if len(rest) < len(n.Path) || !bytes.Equal(n.Path, rest[:len(n.Path)]) {
				return n, rest, false
			}
			rest = rest[len(n.Path):]
			node = n.Child
		case *BranchNode:
			if len(rest) == 0 {
				return n, nil, n.Value != nil
			}
			node = n.Children[rest[0]]
			rest = rest[1:]
		}
	}
	return nil, rest, false
}

// ---------------------------------------------------------------------------
// prefix search
// ---------------------------------------------------------------------------

// GetByPrefix returns every key/value pair whose key starts with prefix.
func (t *Trie) GetByPrefix(prefix string) map[string][]byte {
	out := make(map[string][]byte)
	if t.root == nil {
		return out
	}
	collectByPrefix(t.root, keyToNibbles(prefix), nil, out)
	return out
}

func collectByPrefix(node Node, prefix, acc []byte, out map[string][]byte) {
	switch n := node.(type) {
	case *LeafNode:
		// match if the remaining prefix is a prefix of the leaf path,
		// or the leaf path is a prefix of the remaining prefix.
		if len(prefix) <= len(n.Path) {
			if bytes.Equal(prefix, n.Path[:len(prefix)]) {
				out[nibblesToKey(append(acc, n.Path...))] = n.Value
			}
		} else if bytes.Equal(n.Path, prefix[:len(n.Path)]) {
			out[nibblesToKey(append(acc, n.Path...))] = n.Value
		}
	case *ExtensionNode:
		if len(prefix) <= len(n.Path) {
			if bytes.Equal(prefix, n.Path[:len(prefix)]) {
				collectAll(n.Child, append(acc, n.Path...), out)
			}
			return
		}
		if bytes.Equal(n.Path, prefix[:len(n.Path)]) {
			collectByPrefix(n.Child, prefix[len(n.Path):], append(acc, n.Path...), out)
		}
	case *BranchNode:
		if len(prefix) == 0 {
			collectAll(n, acc, out)
			return
		}
		if child := n.Children[prefix[0]]; child != nil {
			collectByPrefix(child, prefix[1:], append(acc, prefix[0]), out)
		}
	}
}

func collectAll(node Node, acc []byte, out map[string][]byte) {
	switch n := node.(type) {
	case *LeafNode:
		out[nibblesToKey(append(acc, n.Path...))] = n.Value
	case *ExtensionNode:
		collectAll(n.Child, append(acc, n.Path...), out)
	case *BranchNode:
		if n.Value != nil {
			out[nibblesToKey(acc)] = n.Value
		}
		for i, c := range n.Children {
			if c != nil {
				collectAll(c, append(acc, byte(i)), out)
			}
		}
	}
}

// Keys returns all keys in sorted order.
func (t *Trie) Keys() []string {
	all := t.GetByPrefix("")
	keys := make([]string, 0, len(all))
	for k := range all {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ---------------------------------------------------------------------------
// debug dump
// ---------------------------------------------------------------------------

// Dump writes a human-readable rendering of the trie structure.
func (t *Trie) Dump(w io.Writer) {
	if t.root == nil {
		fmt.Fprintln(w, "(empty trie)")
		return
	}
	dumpNode(w, t.root, "")
}

func dumpNode(w io.Writer, node Node, indent string) {
	switch n := node.(type) {
	case *LeafNode:
		fmt.Fprintf(w, "%sLeaf     path=%x value=%q hash=%x\n", indent, n.Path, n.Value, n.Hash()[:8])
	case *ExtensionNode:
		fmt.Fprintf(w, "%sExt      path=%x hash=%x\n", indent, n.Path, n.Hash()[:8])
		dumpNode(w, n.Child, indent+"  ")
	case *BranchNode:
		if n.Value != nil {
			fmt.Fprintf(w, "%sBranch   value=%q hash=%x\n", indent, n.Value, n.Hash()[:8])
		} else {
			fmt.Fprintf(w, "%sBranch   hash=%x\n", indent, n.Hash()[:8])
		}
		for i, c := range n.Children {
			if c != nil {
				fmt.Fprintf(w, "%s  [%x]\n", indent, i)
				dumpNode(w, c, indent+"    ")
			}
		}
	}
}

// KindOf returns a short human-readable name for a node type.
func KindOf(n Node) string {
	switch n.(type) {
	case *LeafNode:
		return "LeafNode"
	case *ExtensionNode:
		return "ExtensionNode"
	case *BranchNode:
		return "BranchNode"
	}
	return "Unknown"
}

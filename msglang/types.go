package msglang

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// LeafKind is the type of a typed leaf symbol.
type LeafKind int

const (
	// Principal is a subject/identity leaf (主体).
	Principal LeafKind = iota
	// Nonce is a random-number leaf (随机数).
	Nonce
	// ByteString is an uninterpreted byte-string leaf (字节串).
	ByteString
)

// Valid reports whether k is one of the declared leaf kinds.
func (k LeafKind) Valid() bool {
	return k == Principal || k == Nonce || k == ByteString
}

// String returns the stable textual name of the leaf kind.
func (k LeafKind) String() string {
	switch k {
	case Principal:
		return "Principal"
	case Nonce:
		return "Nonce"
	case ByteString:
		return "ByteString"
	default:
		return fmt.Sprintf("LeafKind(%d)", int(k))
	}
}

// Leaf is a typed leaf value.
type Leaf struct {
	Kind  LeafKind
	Value []byte
}

// Node is a node of a labeled message tree. A node is either a typed leaf
// (Leaf != nil, no children) or an internal labeled symbol (Leaf == nil).
type Node struct {
	Label    string
	Leaf     *Leaf
	Children []*Node
}

// NewLeaf creates a typed leaf node. The value slice is copied defensively.
func NewLeaf(label string, kind LeafKind, value []byte) *Node {
	return &Node{
		Label: label,
		Leaf: &Leaf{
			Kind:  kind,
			Value: append([]byte(nil), value...),
		},
	}
}

// NewNode creates an internal labeled node over the given children.
func NewNode(label string, children ...*Node) *Node {
	return &Node{Label: label, Children: append([]*Node(nil), children...)}
}

// Clone returns a deep copy of the tree so cached subtrees can be embedded in
// multiple generated trees without sharing node pointers.
func (n *Node) Clone() *Node {
	if n == nil {
		return nil
	}
	if n.IsLeaf() {
		return NewLeaf(n.Label, n.Leaf.Kind, n.Leaf.Value)
	}
	children := make([]*Node, len(n.Children))
	for i, child := range n.Children {
		children[i] = child.Clone()
	}
	return &Node{Label: n.Label, Children: children}
}

func cloneNode(n *Node) *Node {
	return n.Clone()
}

// IsLeaf reports whether the node is a typed leaf.
func (n *Node) IsLeaf() bool {
	return n != nil && n.Leaf != nil
}

// Validate checks that the node is a well-formed finite tree.
func (n *Node) Validate() error {
	seen := make(map[*Node]bool)
	var walk func(*Node) error
	walk = func(node *Node) error {
		if node == nil {
			return fmt.Errorf("msglang: nil tree node")
		}
		if seen[node] {
			return fmt.Errorf("msglang: cyclic tree at label %q", node.Label)
		}
		seen[node] = true
		if node.Label == "" {
			return fmt.Errorf("msglang: empty node label")
		}
		if node.Leaf != nil {
			if !node.Leaf.Kind.Valid() {
				return fmt.Errorf("msglang: leaf %q has invalid kind", node.Label)
			}
			if len(node.Children) != 0 {
				return fmt.Errorf("msglang: leaf %q must not have children", node.Label)
			}
			return nil
		}
		for _, child := range node.Children {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(n)
}

// NodeCount returns the number of nodes in the tree.
func (n *Node) NodeCount() int {
	if n.IsLeaf() {
		return 1
	}
	count := 1
	for _, child := range n.Children {
		count += child.NodeCount()
	}
	return count
}

// Height returns the tree height: leaves have height 0, internal nodes have
// height 1 plus the maximum child height.
func (n *Node) Height() int {
	if n.IsLeaf() {
		return 0
	}
	best := -1
	for _, child := range n.Children {
		if h := child.Height(); h > best {
			best = h
		}
	}
	return best + 1
}

// key builds an unambiguous, collision-free serialization used as a map key.
func (n *Node) key() string {
	var b strings.Builder
	n.writeKey(&b)
	return b.String()
}

func (n *Node) writeKey(b *strings.Builder) {
	if n.IsLeaf() {
		fmt.Fprintf(b, "L%d:%d:%s:%d:", n.Leaf.Kind, len(n.Label), n.Label, len(n.Leaf.Value))
		b.Write(n.Leaf.Value)
		return
	}
	fmt.Fprintf(b, "N%d:%s:%d:", len(n.Label), n.Label, len(n.Children))
	for _, child := range n.Children {
		child.writeKey(b)
	}
}

// String renders the tree as an S-expression.
func (n *Node) String() string {
	var b strings.Builder
	n.writeString(&b)
	return b.String()
}

func (n *Node) writeString(b *strings.Builder) {
	if n.IsLeaf() {
		fmt.Fprintf(b, "%s[%s:%s]", n.Label, n.Leaf.Kind, formatLeafValue(n.Leaf.Value))
		return
	}
	b.WriteString(n.Label)
	b.WriteByte('(')
	for i, child := range n.Children {
		if i > 0 {
			b.WriteByte(' ')
		}
		child.writeString(b)
	}
	b.WriteByte(')')
}

func formatLeafValue(v []byte) string {
	if len(v) == 0 {
		return `""`
	}
	printable := true
	for _, c := range v {
		if c < 0x20 || c > 0x7e {
			printable = false
			break
		}
	}
	if printable {
		return strconv.Quote(string(v))
	}
	return "0x" + hex.EncodeToString(v)
}

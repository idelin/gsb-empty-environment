package main

import (
	"fmt"
	"strings"
)

// Node is an ordered labeled tree node.
type Node struct {
	Label    string  `json:"label"`
	Children []*Node `json:"children,omitempty"`
}

// Clone returns a deep copy of the subtree rooted at n.
func (n *Node) Clone() *Node {
	c := &Node{Label: n.Label}
	for _, ch := range n.Children {
		c.Children = append(c.Children, ch.Clone())
	}
	return c
}

// Equal reports whether a and b are structurally identical (labels and order).
func Equal(a, b *Node) bool {
	if a.Label != b.Label || len(a.Children) != len(b.Children) {
		return false
	}
	for i := range a.Children {
		if !Equal(a.Children[i], b.Children[i]) {
			return false
		}
	}
	return true
}

// Size returns the number of nodes in the subtree rooted at n.
func Size(n *Node) int {
	s := 1
	for _, c := range n.Children {
		s += Size(c)
	}
	return s
}

// CollectLabels adds every label in the subtree to set.
func CollectLabels(n *Node, set map[string]bool) {
	set[n.Label] = true
	for _, c := range n.Children {
		CollectLabels(c, set)
	}
}

// SerializeForest renders a forest in a canonical form, used for structural
// comparison and as a state key in exhaustive search.
func SerializeForest(nodes []*Node) string {
	var sb strings.Builder
	for _, n := range nodes {
		serializeNode(&sb, n)
	}
	return sb.String()
}

func serializeNode(sb *strings.Builder, n *Node) {
	sb.WriteByte('(')
	sb.WriteString(fmt.Sprintf("%q", n.Label))
	for _, c := range n.Children {
		sb.WriteByte(' ')
		serializeNode(sb, c)
	}
	sb.WriteByte(')')
}

func cloneForest(forest []*Node) []*Node {
	out := make([]*Node, len(forest))
	for i, n := range forest {
		out[i] = n.Clone()
	}
	return out
}

func forestSize(forest []*Node) int {
	s := 0
	for _, n := range forest {
		s += Size(n)
	}
	return s
}

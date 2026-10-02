package cteopt

import (
	"strconv"
	"strings"
)

// fingerprint returns a canonical structural key for the subtree rooted at n.
// Two subtrees with equal fingerprints are semantically interchangeable.
func fingerprint(n *Node, memo map[*Node]string) string {
	if s, ok := memo[n]; ok {
		return s
	}
	var b strings.Builder
	b.WriteString(strconv.Itoa(int(n.Op)))
	b.WriteByte('[')
	b.WriteString(n.Label)
	b.WriteByte(']')
	b.WriteByte('(')
	for i, c := range n.Children {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(fingerprint(c, memo))
	}
	b.WriteByte(')')
	s := b.String()
	memo[n] = s
	return s
}

// collect returns every distinct node of the DAG in deterministic order.
func collect(root *Node) []*Node {
	var out []*Node
	seen := map[*Node]bool{}
	var walk func(n *Node)
	walk = func(n *Node) {
		if seen[n] {
			return
		}
		seen[n] = true
		out = append(out, n)
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
	return out
}

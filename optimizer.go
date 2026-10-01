package phyprop

import "strings"

// Enforce walks the plan bottom-up. For each node it first enforces the
// order the node requires from each child, then checks whether the node's
// own delivered order satisfies the requirement coming from its parent.
// If not, a Sort enforcer is inserted on top of the node.
//
// The root call should pass the query's top-level ORDER BY requirement
// (or an empty Order if there is none).
func Enforce(n Node, required Order) Node {
	children := n.Children()
	reqs := n.RequiredProps()
	for i, child := range children {
		children[i] = Enforce(child, reqs[i])
	}
	n.SetChildren(children)

	if required.SatisfiedBy(n.DeliveredProps()) {
		return n
	}
	return NewSort(required, n)
}

// Format renders a plan tree with indentation, for inspection and demos.
func Format(n Node) string {
	var b strings.Builder
	format(&b, n, 0)
	return b.String()
}

func format(b *strings.Builder, n Node, depth int) {
	b.WriteString(strings.Repeat("  ", depth))
	b.WriteString(n.String())
	b.WriteByte('\n')
	for _, c := range n.Children() {
		format(b, c, depth+1)
	}
}

// CountSorts returns how many Sort enforcers exist in the plan tree.
func CountSorts(n Node) int {
	count := 0
	if _, ok := n.(*Sort); ok {
		count++
	}
	for _, c := range n.Children() {
		count += CountSorts(c)
	}
	return count
}

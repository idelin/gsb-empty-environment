package earley

import (
	"fmt"
	"strings"
)

// Node is one node of a parse tree. Terminals carry Value; non-terminals
// carry Children (empty for an epsilon production).
type Node struct {
	Sym      string
	Value    string
	Children []*Node
}

// String renders the tree as an S-expression, e.g. (E (E a) + (E b)).
func (n *Node) String() string {
	if len(n.Children) == 0 {
		if n.Value != "" {
			return n.Value
		}
		return "(" + n.Sym + ")"
	}
	parts := make([]string, len(n.Children))
	for i, c := range n.Children {
		parts[i] = c.String()
	}
	return "(" + n.Sym + " " + strings.Join(parts, " ") + ")"
}

// indent renders the tree one node per line for human inspection.
func (n *Node) indent(depth int) string {
	pad := strings.Repeat("  ", depth)
	label := n.Sym
	if n.Value != "" {
		label = fmt.Sprintf("%s(%q)", n.Sym, n.Value)
	}
	out := pad + label + "\n"
	for _, c := range n.Children {
		out += c.indent(depth + 1)
	}
	return out
}

// Pretty renders the tree as an indented node-per-line listing.
func (n *Node) Pretty() string { return n.indent(0) }

// ParseTrees enumerates every parse tree licensed by the chart. For an
// ambiguous grammar the result contains one entry per distinct parse.
func (c *Chart) ParseTrees() []*Node {
	final := c.finalState()
	if final == nil {
		return nil
	}
	f := &forest{chart: c, memo: make(map[*state][][]*Node)}
	var trees []*Node
	seen := make(map[string]bool)
	for _, children := range f.expand(final) {
		// children is the single-child list [S-tree] of S' -> S.
		for _, t := range children {
			key := t.String()
			if !seen[key] {
				seen[key] = true
				trees = append(trees, t)
			}
		}
	}
	return trees
}

// forest enumerates trees from the packed backpointer graph, memoizing
// expansions per state so shared sub-derivations are computed once.
type forest struct {
	chart *Chart
	memo  map[*state][][]*Node
}

// expand returns, for state st, every list of child nodes that derives
// the portion of the rule to the left of st's dot.
func (f *forest) expand(st *state) [][]*Node {
	if st.dot == 0 {
		return [][]*Node{{}}
	}
	if m, ok := f.memo[st]; ok {
		return m
	}
	var out [][]*Node
	for _, b := range st.backs {
		prefixes := f.expand(b.pred)
		var lasts []*Node
		if b.child != nil {
			lasts = f.subtrees(b.child)
		} else {
			lasts = []*Node{{Sym: b.term.Type, Value: b.term.Value}}
		}
		for _, pre := range prefixes {
			for _, last := range lasts {
				next := make([]*Node, len(pre)+1)
				copy(next, pre)
				next[len(pre)] = last
				out = append(out, next)
			}
		}
	}
	f.memo[st] = out
	return out
}

// subtrees returns one tree per expansion of a completed state.
func (f *forest) subtrees(st *state) []*Node {
	g := f.chart.grammar
	var out []*Node
	for _, children := range f.expand(st) {
		out = append(out, &Node{Sym: string(g.Rules[st.rule].LHS), Children: children})
	}
	return out
}

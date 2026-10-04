package ambcheck

import (
	"fmt"
	"strings"
)

// Tree is a parse tree. Interior nodes store the production used (LHS and
// the RHS symbols, by content — not by rule index, so duplicate rules yield
// identical trees); leaves store a terminal. Children of a node correspond
// one-to-one to the RHS symbols. Distinct trees built by the checker share
// child pointers, forming a shared parse forest.
type Tree struct {
	LHS      string   `json:"lhs,omitempty"`      // nonterminal at this node (interior)
	RHS      []string `json:"rhs,omitempty"`      // production RHS used (interior)
	Terminal string   `json:"terminal,omitempty"` // terminal symbol (leaf)
	Children []*Tree  `json:"children,omitempty"`
}

// Leaf returns a terminal leaf node.
func Leaf(terminal string) *Tree { return &Tree{Terminal: terminal} }

// Node returns an interior node for production lhs -> rhs.
func Node(lhs string, rhs []string, children ...*Tree) *Tree {
	return &Tree{LHS: lhs, RHS: rhs, Children: children}
}

// IsLeaf reports whether t is a terminal leaf.
func (t *Tree) IsLeaf() bool { return t.Children == nil && t.RHS == nil }

// Canonical returns a structural encoding of the tree. Two trees have the
// same canonical form iff they are structurally identical (same production
// contents at every node), regardless of how they were constructed. This is
// the deduplication key of the shared parse forest.
func (t *Tree) Canonical() string {
	var sb strings.Builder
	t.writeCanonical(&sb)
	return sb.String()
}

func (t *Tree) writeCanonical(sb *strings.Builder) {
	if t.IsLeaf() {
		fmt.Fprintf(sb, "%d:%s", len(t.Terminal), t.Terminal)
		return
	}
	fmt.Fprintf(sb, "%d:%s[", len(t.LHS), t.LHS)
	for _, s := range t.RHS {
		fmt.Fprintf(sb, "%d:%s,", len(s), s)
	}
	sb.WriteByte(']')
	sb.WriteByte('(')
	for _, c := range t.Children {
		c.writeCanonical(sb)
		sb.WriteByte(';')
	}
	sb.WriteByte(')')
}

// Yield returns the terminal string derived by the tree, left to right.
func (t *Tree) Yield() []string {
	var out []string
	var walk func(n *Tree)
	walk = func(n *Tree) {
		if n.IsLeaf() {
			out = append(out, n.Terminal)
			return
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(t)
	return out
}

// String renders the tree as an S-expression for human inspection.
func (t *Tree) String() string {
	var sb strings.Builder
	var walk func(n *Tree, depth int)
	walk = func(n *Tree, depth int) {
		indent := strings.Repeat("  ", depth)
		if n.IsLeaf() {
			fmt.Fprintf(&sb, "%s%q\n", indent, n.Terminal)
			return
		}
		fmt.Fprintf(&sb, "%s(%s -> %s)\n", indent, n.LHS, strings.Join(n.RHS, " "))
		for _, c := range n.Children {
			walk(c, depth+1)
		}
	}
	walk(t, 0)
	return sb.String()
}

// VerifyTree independently checks that t is a valid parse tree of g and
// returns its yield. Every interior node must use a production present in
// the grammar (matched by content) and every leaf must be a declared
// terminal.
func VerifyTree(g *Grammar, t *Tree) ([]string, error) {
	if err := g.Validate(); err != nil {
		return nil, err
	}
	term := make(map[string]bool, len(g.Terminals))
	for _, s := range g.Terminals {
		term[s] = true
	}
	nonterm := make(map[string]bool, len(g.NonTerminals))
	for _, s := range g.NonTerminals {
		nonterm[s] = true
	}
	ruleOK := make(map[string]bool, len(g.Rules))
	for _, r := range g.Rules {
		ruleOK[ruleKey(r.LHS, r.RHS)] = true
	}
	var walk func(n *Tree) ([]string, error)
	walk = func(n *Tree) ([]string, error) {
		if n == nil {
			return nil, fmt.Errorf("nil node")
		}
		if n.IsLeaf() {
			if !term[n.Terminal] {
				return nil, fmt.Errorf("leaf %q is not a terminal", n.Terminal)
			}
			return []string{n.Terminal}, nil
		}
		if !nonterm[n.LHS] {
			return nil, fmt.Errorf("node LHS %q is not a nonterminal", n.LHS)
		}
		if !ruleOK[ruleKey(n.LHS, n.RHS)] {
			return nil, fmt.Errorf("no production %s -> %s in grammar", n.LHS, strings.Join(n.RHS, " "))
		}
		if len(n.Children) != len(n.RHS) {
			return nil, fmt.Errorf("node %s has %d children for %d RHS symbols", n.LHS, len(n.Children), len(n.RHS))
		}
		var yield []string
		for i, sym := range n.RHS {
			child := n.Children[i]
			if term[sym] {
				if !child.IsLeaf() || child.Terminal != sym {
					return nil, fmt.Errorf("node %s: child %d must be terminal %q", n.LHS, i, sym)
				}
				yield = append(yield, sym)
				continue
			}
			if child.IsLeaf() || child.LHS != sym {
				return nil, fmt.Errorf("node %s: child %d must be rooted at %q", n.LHS, i, sym)
			}
			sub, err := walk(child)
			if err != nil {
				return nil, err
			}
			yield = append(yield, sub...)
		}
		return yield, nil
	}
	yield, err := walk(t)
	if err != nil {
		return nil, err
	}
	if t.IsLeaf() || t.LHS != g.Start {
		return nil, fmt.Errorf("tree is not rooted at start symbol %q", g.Start)
	}
	return yield, nil
}

func ruleKey(lhs string, rhs []string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d:%s", len(lhs), lhs)
	for _, s := range rhs {
		fmt.Fprintf(&sb, "%d:%s,", len(s), s)
	}
	return sb.String()
}

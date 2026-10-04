package ambiguity

import (
	"fmt"
	"strings"
)

// Budget caps the number of expanded states. A "state" is one attempt to
// match one production against one composition of one span. A negative
// limit means unlimited.
type Budget struct {
	Limit int
	Used  int
}

// spend consumes one state; it reports false when the budget is exhausted.
func (b *Budget) spend() bool {
	if b.Limit >= 0 && b.Used >= b.Limit {
		return false
	}
	b.Used++
	return true
}

// Node is a shared SPPF node: all parse subtrees deriving s[i:j] from Sym
// are represented once, by this node and its packed-node alternatives.
type Node struct {
	Sym       Symbol
	I, J      int
	leaf      bool
	packed    []packedNode
	packedSet map[string]bool
}

// packedNode is one way to build the parent node: a production applied to
// a specific split, i.e. a fixed tuple of child nodes. Identical
// constructions collapse into a single packed node, so two derivation
// paths that build the same tree are never counted twice.
type packedNode struct {
	prod     Production
	children []*Node
}

// packedKeyOf identifies a packed node by the production's content (not
// its index, so duplicate rules collapse) and the identity of its
// canonical child nodes.
func packedKeyOf(p Production, children []*Node) string {
	var b strings.Builder
	b.WriteString(string(p.LHS))
	b.WriteString("->")
	for _, s := range p.RHS {
		b.WriteString(string(s))
		b.WriteByte(',')
	}
	for _, c := range children {
		fmt.Fprintf(&b, ":%p", c)
	}
	return b.String()
}

// forest is one CYK-style recognition pass over a single string.
type forest struct {
	g      *Grammar
	s      []Symbol
	nodes  map[nodeKey]*Node
	budget *Budget
}

type nodeKey struct {
	sym  Symbol
	i, j int
}

// buildForest parses s with g, spending from budget. It returns the root
// node (nil if s is not derivable) and false if the budget ran out.
func buildForest(g *Grammar, s []Symbol, budget *Budget) (*Node, bool) {
	f := &forest{g: g, s: s, nodes: map[nodeKey]*Node{}, budget: budget}
	n := len(s)
	for i, t := range s {
		f.nodes[nodeKey{t, i, i + 1}] = &Node{Sym: t, I: i, J: i + 1, leaf: true}
	}
	for span := 1; span <= n; span++ {
		for i := 0; i+span <= n; i++ {
			j := i + span
			if _, ok := f.fillSpan(i, j, false); !ok {
				return nil, false
			}
			// Unit productions (A -> B) reference nodes of the same span,
			// so they need a fixpoint within the span.
			for {
				changed, ok := f.fillSpan(i, j, true)
				if !ok {
					return nil, false
				}
				if !changed {
					break
				}
			}
		}
	}
	return f.nodes[nodeKey{g.Start, 0, n}], true
}

// fillSpan tries every production (or only unit productions when unitOnly)
// against every composition of [i,j). It reports whether any packed node
// was added and whether the budget held.
func (f *forest) fillSpan(i, j int, unitOnly bool) (bool, bool) {
	changed := false
	for _, p := range f.g.Productions {
		k := len(p.RHS)
		if unitOnly && !(k == 1 && f.g.Nonterminals[p.RHS[0]]) {
			continue
		}
		if k > j-i {
			continue
		}
		ok := true
		eachComposition(i, j, k, func(bounds []int) bool {
			if !f.budget.spend() {
				ok = false
				return false
			}
			children := make([]*Node, k)
			good := true
			for m := 0; m < k; m++ {
				cn := f.nodes[nodeKey{p.RHS[m], bounds[m], bounds[m+1]}]
				if cn == nil {
					good = false
					break
				}
				children[m] = cn
			}
			if good && f.addPacked(p.LHS, i, j, p, children) {
				changed = true
			}
			return true
		})
		if !ok {
			return changed, false
		}
	}
	return changed, true
}

// addPacked records one construction of sym over [i,j); it reports whether
// it was new. Terminal children only exist as leaves at matching
// positions, so a terminal spanning more than one cell simply finds no
// child node.
func (f *forest) addPacked(sym Symbol, i, j int, p Production, children []*Node) bool {
	key := nodeKey{sym, i, j}
	n := f.nodes[key]
	if n == nil {
		n = &Node{Sym: sym, I: i, J: j, packedSet: map[string]bool{}}
		f.nodes[key] = n
	}
	pk := packedKeyOf(p, children)
	if n.packedSet[pk] {
		return false
	}
	n.packedSet[pk] = true
	n.packed = append(n.packed, packedNode{prod: p, children: children})
	return true
}

// eachComposition calls fn with every split of [i,j) into k non-empty
// parts, as k+1 boundary indices. fn returning false aborts the walk.
func eachComposition(i, j, k int, fn func(bounds []int) bool) {
	bounds := make([]int, k+1)
	bounds[0] = i
	bounds[k] = j
	var rec func(m, lo int) bool
	rec = func(m, lo int) bool {
		if m == k {
			return fn(bounds)
		}
		hi := j - (k - m) // leave at least one cell per remaining part
		for p := lo + 1; p <= hi; p++ {
			bounds[m] = p
			if !rec(m+1, p) {
				return false
			}
		}
		return true
	}
	rec(1, i)
}

// countTrees counts the distinct parse trees below n, capped at cap.
// Because equal subtrees are shared, summing over packed nodes and
// multiplying over children counts each distinct tree exactly once.
func countTrees(n *Node, cap int, memo map[*Node]int) int {
	if n.leaf {
		return 1
	}
	if v, ok := memo[n]; ok {
		return v
	}
	total := 0
	for _, pk := range n.packed {
		prod := 1
		for _, c := range pk.children {
			prod *= countTrees(c, cap, memo)
			if prod >= cap {
				prod = cap
				break
			}
		}
		total += prod
		if total >= cap {
			total = cap
			break
		}
	}
	memo[n] = total
	return total
}

// extractTrees materializes up to limit distinct parse trees below n.
func extractTrees(n *Node, limit int) []*Tree {
	if n.leaf {
		return []*Tree{{Sym: n.Sym}}
	}
	var out []*Tree
	for _, pk := range n.packed {
		combos := [][]*Tree{{}}
		for _, c := range pk.children {
			ct := extractTrees(c, limit)
			var next [][]*Tree
			for _, prefix := range combos {
				for _, t := range ct {
					cp := make([]*Tree, len(prefix)+1)
					copy(cp, prefix)
					cp[len(prefix)] = t
					next = append(next, cp)
					if len(next) >= limit {
						break
					}
				}
				if len(next) >= limit {
					break
				}
			}
			combos = next
		}
		for _, combo := range combos {
			out = append(out, &Tree{Sym: n.Sym, Children: combo})
			if len(out) >= limit {
				return out
			}
		}
	}
	return out
}

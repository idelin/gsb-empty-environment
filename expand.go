package uniparse

import (
	"fmt"
	"strings"
)

// Tree is one fully expanded derivation.
type Tree struct {
	Sym      Symbol
	Rule     *Rule // nil for terminal leaves
	Children []*Tree
}

// Yield returns the terminal words of the tree, left to right.
func (t *Tree) Yield() []string {
	if t.Rule == nil {
		return []string{t.Sym.Name}
	}
	var out []string
	for _, c := range t.Children {
		out = append(out, c.Yield()...)
	}
	return out
}

func (t *Tree) String() string {
	var b strings.Builder
	t.write(&b, 0)
	return b.String()
}

func (t *Tree) write(b *strings.Builder, depth int) {
	for i := 0; i < depth; i++ {
		b.WriteString("  ")
	}
	b.WriteString(t.Sym.String())
	b.WriteByte('\n')
	for _, c := range t.Children {
		c.write(b, depth+1)
	}
}

func (t *Tree) key() string {
	var b strings.Builder
	t.keyInto(&b)
	return b.String()
}

func (t *Tree) keyInto(b *strings.Builder) {
	b.WriteByte('(')
	b.WriteString(t.Sym.String())
	if t.Rule != nil {
		fmt.Fprintf(b, "<%s>", t.Rule)
	}
	for _, c := range t.Children {
		c.keyInto(b)
	}
	b.WriteByte(')')
}

// Expand enumerates up to max distinct derivation trees for n. Recursive
// (cyclic) forests are handled by iterative deepening, so enumeration
// always terminates; if fewer than max trees come back and the forest is
// cyclic, more trees simply exist beyond the budget.
func Expand(n *Node, max int) []*Tree {
	if max <= 0 || n == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []*Tree
	for depth := 1; depth < 1<<20; depth++ {
		trees, cut := expandDepth(n, depth, max)
		for _, t := range trees {
			k := t.key()
			if !seen[k] {
				seen[k] = true
				out = append(out, t)
			}
		}
		if !cut || len(out) >= max {
			break
		}
	}
	if len(out) > max {
		out = out[:max]
	}
	return out
}

// expandDepth returns up to max trees of n whose expansion stays within
// depth. The second result reports that some branch was cut by the depth
// limit, so a deeper pass may find more trees. Hitting the max cap is not
// reported: the caller only asked for max trees anyway.
func expandDepth(n *Node, depth, max int) ([]*Tree, bool) {
	if n.Sym.Terminal {
		return []*Tree{{Sym: n.Sym}}, false
	}
	if depth <= 0 {
		return nil, true
	}
	var out []*Tree
	cut := false
	for _, pk := range n.Packings {
		if len(out) >= max {
			break
		}
		if !packingConsistent(n, pk) {
			continue
		}
		lists := make([][]*Tree, len(pk.Children))
		viable := true
		for i, c := range pk.Children {
			if c.Sym.Terminal {
				lists[i] = []*Tree{{Sym: c.Sym}}
				continue
			}
			cts, tr := expandDepth(c, depth-1, max)
			if tr {
				cut = true
			}
			if len(cts) == 0 {
				viable = false
				break
			}
			lists[i] = cts
		}
		if !viable {
			continue
		}
		combos, _ := product(lists, max-len(out))
		for _, combo := range combos {
			out = append(out, &Tree{Sym: n.Sym, Rule: pk.Rule, Children: combo})
		}
	}
	return out, cut
}

// packingConsistent re-runs unification for one packing with completely
// fresh variables: the rule instance, the node features and every child
// node's features must have a common unifier.
func packingConsistent(n *Node, pk *Packing) bool {
	if len(pk.Children) != len(pk.Rule.RHS) {
		return false
	}
	ren := &renamer{}
	m := map[string]Value{}
	s, ok := unifyFeats(subst{}, instFeats(pk.Rule.LHS.Feats, ren, m), instFeats(n.Sym.Feats, ren, map[string]Value{}))
	if !ok {
		return false
	}
	for i, c := range pk.Children {
		rs := pk.Rule.RHS[i]
		if rs.Terminal {
			if !c.Sym.Terminal || c.Sym.Name != rs.Name {
				return false
			}
			continue
		}
		if c.Sym.Terminal || c.Sym.Name != rs.Name {
			return false
		}
		s, ok = unifyFeats(s, instFeats(rs.Feats, ren, m), instFeats(c.Sym.Feats, ren, map[string]Value{}))
		if !ok {
			return false
		}
	}
	return true
}

// product returns the cartesian product of child tree lists, capped at
// limit combinations.
func product(lists [][]*Tree, limit int) ([][]*Tree, bool) {
	out := [][]*Tree{{}}
	truncated := false
	for _, l := range lists {
		var next [][]*Tree
		for _, combo := range out {
			for _, t := range l {
				if len(next) >= limit {
					truncated = true
					break
				}
				nc := make([]*Tree, len(combo)+1)
				copy(nc, combo)
				nc[len(combo)] = t
				next = append(next, nc)
			}
			if truncated {
				break
			}
		}
		out = next
		if truncated {
			break
		}
	}
	return out, truncated
}

// Trees expands every root and returns up to max distinct derivation trees.
func (f *Forest) Trees(max int) []*Tree {
	var out []*Tree
	seen := map[string]bool{}
	for _, r := range f.Roots {
		for _, t := range Expand(r, max) {
			if len(out) >= max {
				return out
			}
			if k := t.key(); !seen[k] {
				seen[k] = true
				out = append(out, t)
			}
		}
	}
	return out
}

// Verify independently expands every root (at most maxTrees trees per
// root) and re-checks each tree's word order and feature consistency.
// It returns the number of trees verified.
func (f *Forest) Verify(maxTrees int) (int, error) {
	total := 0
	for _, r := range f.Roots {
		for _, t := range Expand(r, maxTrees) {
			if err := VerifyTree(t, f.Words); err != nil {
				return total, err
			}
			total++
		}
	}
	return total, nil
}

// VerifyTree independently re-checks one expanded tree: every internal
// node must be justified by its rule under a fresh unification with its
// children, and the terminal yield must equal words.
func VerifyTree(t *Tree, words []string) error {
	yield, err := verifyNode(t)
	if err != nil {
		return err
	}
	if !equalWords(yield, words) {
		return fmt.Errorf("uniparse: yield %q does not match input %q", yield, words)
	}
	return nil
}

func verifyNode(t *Tree) ([]string, error) {
	if t.Rule == nil {
		if !t.Sym.Terminal {
			return nil, fmt.Errorf("uniparse: nonterminal leaf %s", t.Sym)
		}
		return []string{t.Sym.Name}, nil
	}
	r := t.Rule
	if t.Sym.Terminal || t.Sym.Name != r.LHS.Name {
		return nil, fmt.Errorf("uniparse: node %s does not match rule %s", t.Sym, r)
	}
	if len(t.Children) != len(r.RHS) {
		return nil, fmt.Errorf("uniparse: node %s has %d children, rule %s has %d", t.Sym, len(t.Children), r, len(r.RHS))
	}
	ren := &renamer{}
	m := map[string]Value{}
	s, ok := unifyFeats(subst{}, instFeats(r.LHS.Feats, ren, m), instFeats(t.Sym.Feats, ren, map[string]Value{}))
	if !ok {
		return nil, fmt.Errorf("uniparse: node %s features inconsistent with rule %s", t.Sym, r)
	}
	var yield []string
	for i, c := range t.Children {
		rs := r.RHS[i]
		if rs.Terminal {
			if c.Rule != nil || !c.Sym.Terminal || c.Sym.Name != rs.Name {
				return nil, fmt.Errorf("uniparse: child %d of %s is not terminal %q", i, t.Sym, rs.Name)
			}
		} else {
			if c.Sym.Terminal || c.Sym.Name != rs.Name {
				return nil, fmt.Errorf("uniparse: child %d of %s is not %s", i, t.Sym, rs.Name)
			}
			s, ok = unifyFeats(s, instFeats(rs.Feats, ren, m), instFeats(c.Sym.Feats, ren, map[string]Value{}))
			if !ok {
				return nil, fmt.Errorf("uniparse: child %d (%s) of %s has inconsistent features", i, c.Sym, t.Sym)
			}
		}
		cy, err := verifyNode(c)
		if err != nil {
			return nil, err
		}
		yield = append(yield, cy...)
	}
	return yield, nil
}

func equalWords(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

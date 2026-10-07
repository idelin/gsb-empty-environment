package fparse

import (
	"fmt"
	"strings"
)

// Forest is the shared (packed) parse forest for one input: every root is
// a packed start-category node spanning the whole word sequence.
type Forest struct {
	Words []string
	Start string
	Roots []*Node
	rules []*Rule
}

// DefaultMaxTrees bounds how many trees Trees/Verify expand.
const DefaultMaxTrees = 4096

// NumAnalyses counts the total number of analyses packed in the forest
// (the product of alternative counts along every path), capped at max.
func (f *Forest) NumAnalyses(max int) int {
	total := 0
	memo := map[*Node]int{}
	var count func(n *Node) int
	count = func(n *Node) int {
		if c, ok := memo[n]; ok {
			return c
		}
		c := 0
		for _, alt := range n.Alts {
			ways := 1
			for _, ch := range alt.Children {
				ways *= count(ch)
			}
			c += ways
			if c > max {
				c = max
				break
			}
		}
		memo[n] = c
		return c
	}
	for _, r := range f.Roots {
		total += count(r)
		if total > max {
			return max
		}
	}
	return total
}

// Tree is one fully expanded analysis. Terminal leaves carry Word and
// have RuleID == -1.
type Tree struct {
	Cat      string
	Features FeatureMap
	Word     string
	RuleID   int
	Children []*Tree
}

// Yield returns the surface words of the tree, left to right.
func (t *Tree) Yield() []string {
	if t.RuleID < 0 {
		return []string{t.Word}
	}
	var out []string
	for _, c := range t.Children {
		out = append(out, c.Yield()...)
	}
	return out
}

func (t *Tree) key() string {
	if t.RuleID < 0 {
		return "w:" + t.Word
	}
	var b strings.Builder
	b.WriteString(t.Cat)
	b.WriteString(t.Features.key())
	b.WriteByte('(')
	for _, c := range t.Children {
		b.WriteString(c.key())
		b.WriteByte(',')
	}
	b.WriteByte(')')
	return b.String()
}

func (t *Tree) String() string {
	var b strings.Builder
	t.write(&b, 0)
	return b.String()
}

func (t *Tree) write(b *strings.Builder, depth int) {
	indent := strings.Repeat("  ", depth)
	if t.RuleID < 0 {
		fmt.Fprintf(b, "%s%q\n", indent, t.Word)
		return
	}
	fmt.Fprintf(b, "%s%s%s\n", indent, t.Cat, t.Features)
	for _, c := range t.Children {
		c.write(b, depth+1)
	}
}

// Trees independently expands the packed forest into individual trees,
// at most max of them (pass DefaultMaxTrees if unsure). Trees that are
// equivalent up to variable renaming are deduplicated.
func (f *Forest) Trees(max int) []*Tree {
	if max <= 0 {
		max = DefaultMaxTrees
	}
	budget := max
	var out []*Tree
	seen := map[string]bool{}
	for _, root := range f.Roots {
		for _, t := range f.expandNode(root, map[*Node]bool{}, &budget) {
			k := t.key()
			if !seen[k] {
				seen[k] = true
				out = append(out, t)
			}
		}
	}
	return out
}

// expandNode unfolds one packed node into trees. The path set guards
// against cyclic forests (which can only arise from unary-rule cycles).
func (f *Forest) expandNode(n *Node, path map[*Node]bool, budget *int) []*Tree {
	if *budget <= 0 || path[n] {
		return nil
	}
	path[n] = true
	defer delete(path, n)
	var trees []*Tree
	for _, alt := range n.Alts {
		if *budget <= 0 {
			break
		}
		rule := f.rules[alt.RuleID]
		combos := []*Tree{{Cat: n.Cat, Features: n.Features, RuleID: alt.RuleID}}
		ci := 0
		for _, it := range rule.RHS {
			var next []*Tree
			if it.Terminal {
				leaf := &Tree{Word: it.Name, RuleID: -1}
				for _, c := range combos {
					c2 := *c
					c2.Children = append(append([]*Tree(nil), c.Children...), leaf)
					next = append(next, &c2)
				}
			} else {
				childTrees := f.expandNode(alt.Children[ci], path, budget)
				ci++
				for _, c := range combos {
					for _, ct := range childTrees {
						c2 := *c
						c2.Children = append(append([]*Tree(nil), c.Children...), ct)
						next = append(next, &c2)
					}
				}
			}
			combos = next
		}
		*budget -= len(combos)
		trees = append(trees, combos...)
	}
	return trees
}

// Verify independently re-checks every expanded tree (up to maxTrees):
// each tree's leaf yield must equal the input word sequence, and every
// node's features must be re-derivable by instantiating its rule with
// fresh variables and unifying with exactly the features of its children
// in this tree — so any pollution of features across packed branches is
// detected here.
func (f *Forest) Verify(maxTrees int) error {
	if maxTrees <= 0 {
		maxTrees = DefaultMaxTrees
	}
	want := strings.Join(f.Words, " ")
	for i, t := range f.Trees(maxTrees) {
		if got := strings.Join(t.Yield(), " "); got != want {
			return fmt.Errorf("tree %d: yield %q does not match input %q", i, got, want)
		}
		if err := f.verifyTree(t); err != nil {
			return fmt.Errorf("tree %d: %w", i, err)
		}
	}
	return nil
}

// verifyTree replays unification bottom-up for one expanded tree.
func (f *Forest) verifyTree(t *Tree) error {
	if t.RuleID < 0 {
		return nil
	}
	rule := f.rules[t.RuleID]
	if len(t.Children) != len(rule.RHS) {
		return fmt.Errorf("%s: rule %d has %d RHS items but tree has %d children",
			t.Cat, t.RuleID, len(rule.RHS), len(t.Children))
	}
	var gen varGen
	fr := gen.newFreshener()
	subst := Subst{}
	for k, it := range rule.RHS {
		ch := t.Children[k]
		if it.Terminal {
			if ch.RuleID != -1 || ch.Word != it.Name {
				return fmt.Errorf("%s: RHS item %d should be terminal %q", t.Cat, k, it.Name)
			}
			continue
		}
		if ch.Cat != it.Name {
			return fmt.Errorf("%s: child %d is %s, rule expects %s", t.Cat, k, ch.Cat, it.Name)
		}
		itemFeat := fr.freshen(it.Features)
		childFeat := gen.freshen(ch.Features)
		_, ns, ok := unifyFeatures(itemFeat, childFeat, subst)
		if !ok {
			return fmt.Errorf("%s: features of child %d (%s%s) do not unify with the rule",
				t.Cat, k, ch.Cat, ch.Features)
		}
		subst = ns
	}
	// The features derived for the LHS must be exactly the features
	// stored at this tree node, up to variable renaming.
	derived := canonical(fr.freshen(rule.LHS.Features), subst)
	if derived.key() != t.Features.key() {
		return fmt.Errorf("%s: derived features %s do not match stored %s",
			t.Cat, derived, t.Features)
	}
	for _, ch := range t.Children {
		if err := f.verifyTree(ch); err != nil {
			return err
		}
	}
	return nil
}

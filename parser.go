package fparse

import (
	"fmt"
	"strings"
)

// Node is a packed forest node: one category plus canonical feature map
// over one input span, with all alternative derivations packed inside.
// Nodes are deduplicated by (category, canonical features, span), so
// analyses that differ only by a renaming of variables share one node.
type Node struct {
	Cat      string
	Features FeatureMap // canonical form
	Start    int
	End      int
	Alts     []*Alternative
}

// Alternative is one derivation of a Node: a rule instance whose
// nonterminal children are themselves packed nodes, in RHS order
// (terminals of the rule are implicit, recovered from the rule itself).
type Alternative struct {
	RuleID   int
	Children []*Node
}

func (n *Node) key() string {
	return fmt.Sprintf("%s%s@%d-%d", n.Cat, n.Features.key(), n.Start, n.End)
}

func (a *Alternative) key() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d(", a.RuleID)
	for _, c := range a.Children {
		b.WriteString(c.key())
		b.WriteByte(',')
	}
	b.WriteByte(')')
	return b.String()
}

// parser is a bottom-up chart parser. Because the grammar is
// epsilon-free, every constituent covers a non-empty span and spans are
// processed by increasing length, so arbitrary recursion (left or right)
// is handled naturally.
type parser struct {
	g     *Grammar
	words []string
	n     int
	cells [][][]*Node          // cells[start][end] -> packed nodes, in insertion order
	index [][]map[string]*Node // cells[start][end] keyed by node key
	gen   varGen
}

// ParseForest parses words with the grammar and returns the shared forest
// of all unifiable analyses rooted at the start category. The input must
// contain between 1 and MaxWords words.
func (g *Grammar) ParseForest(words []string, start string) (*Forest, error) {
	if len(words) == 0 {
		return nil, fmt.Errorf("no input words")
	}
	if len(words) > MaxWords {
		return nil, fmt.Errorf("input too long: %d words, maximum is %d", len(words), MaxWords)
	}
	n := len(words)
	p := &parser{
		g:     g,
		words: words,
		n:     n,
		cells: make([][][]*Node, n+1),
		index: make([][]map[string]*Node, n+1),
	}
	for i := range p.cells {
		p.cells[i] = make([][]*Node, n+1)
		p.index[i] = make([]map[string]*Node, n+1)
		for j := range p.cells[i] {
			p.index[i][j] = map[string]*Node{}
		}
	}
	p.run()
	var roots []*Node
	for _, node := range p.cells[0][n] {
		if node.Cat == start {
			roots = append(roots, node)
		}
	}
	return &Forest{Words: words, Start: start, Roots: roots, rules: g.Rules}, nil
}

func (p *parser) run() {
	for length := 1; length <= p.n; length++ {
		for i := 0; i+length <= p.n; i++ {
			j := i + length
			// Fixpoint within the cell so that unary chains (A -> B)
			// saturate before longer spans are built. Termination is
			// guaranteed: only finitely many distinct canonical
			// (category, features) pairs exist for a given grammar.
			for {
				added := false
				for _, r := range p.g.Rules {
					if p.matchRule(r, i, j) {
						added = true
					}
				}
				if !added {
					break
				}
			}
		}
	}
}

// matchRule instantiates r with fresh variables and adds every analysis
// of r covering exactly the span [i,j). It reports whether anything new
// (node or alternative) was added.
func (p *parser) matchRule(r *Rule, i, j int) bool {
	// One freshener for the whole rule: a rule-local variable keeps a
	// single fresh identity across the LHS and every RHS item.
	fr := p.gen.newFreshener()
	lhsFeat := fr.freshen(r.LHS.Features)
	itemFeats := make([]FeatureMap, len(r.RHS))
	for k, it := range r.RHS {
		itemFeats[k] = fr.freshen(it.Features)
	}
	added := false
	var rec func(pos, idx int, subst Subst, children []*Node)
	rec = func(pos, idx int, subst Subst, children []*Node) {
		if idx == len(r.RHS) {
			if pos != j {
				return
			}
			if p.addNode(r, i, j, canonical(lhsFeat, subst), children) {
				added = true
			}
			return
		}
		it := r.RHS[idx]
		if it.Terminal {
			if pos < j && p.words[pos] == it.Name {
				rec(pos+1, idx+1, subst, children)
			}
			return
		}
		for k := pos + 1; k <= j; k++ {
			// Snapshot the cell: additions during the loop are picked
			// up by the outer fixpoint iteration instead.
			cands := p.cells[pos][k]
			for _, cand := range cands {
				if cand.Cat != it.Name {
					continue
				}
				// Freshen the stored node's variables so that
				// canonical variables of different nodes can never
				// be accidentally identified across branches.
				candFeat := p.gen.freshen(cand.Features)
				if _, ns, ok := unifyFeatures(itemFeats[idx], candFeat, subst); ok {
					rec(k, idx+1, ns, append(children, cand))
				}
			}
		}
	}
	rec(i, 0, Subst{}, nil)
	return added
}

// addNode inserts a packed node (or one more alternative into an existing
// node) and reports whether the chart changed.
func (p *parser) addNode(r *Rule, i, j int, feat FeatureMap, children []*Node) bool {
	alt := &Alternative{RuleID: r.ID, Children: append([]*Node(nil), children...)}
	n := &Node{Cat: r.LHS.Name, Features: feat, Start: i, End: j}
	key := n.key()
	if existing, ok := p.index[i][j][key]; ok {
		ak := alt.key()
		for _, a := range existing.Alts {
			if a.key() == ak {
				return false
			}
		}
		existing.Alts = append(existing.Alts, alt)
		return true
	}
	n.Alts = []*Alternative{alt}
	p.cells[i][j] = append(p.cells[i][j], n)
	p.index[i][j][key] = n
	return true
}

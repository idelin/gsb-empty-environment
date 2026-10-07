package uniparse

import (
	"fmt"
	"sort"
	"strings"
)

// Node is a packed-forest node: a category (with canonical features)
// recognized over Words[Start:End]. Packings lists every way the node can
// be derived; derivations shared below this node are represented once.
type Node struct {
	Sym      Symbol
	Start    int
	End      int
	Packings []*Packing

	id       int
	packKeys map[string]bool
}

// Packing is one derivation of a Node: a rule applied to child nodes.
type Packing struct {
	Rule     *Rule
	Children []*Node
}

// Forest is the shared parse forest for one input.
type Forest struct {
	Words []string
	Roots []*Node

	nodes []*Node
}

// Nodes returns every nonterminal node in the forest, sorted by span,
// name, and features for determinism.
func (f *Forest) Nodes() []*Node {
	out := make([]*Node, len(f.nodes))
	copy(out, f.nodes)
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Start != b.Start {
			return a.Start < b.Start
		}
		if a.End != b.End {
			return a.End < b.End
		}
		if a.Sym.Name != b.Sym.Name {
			return a.Sym.Name < b.Sym.Name
		}
		return featsKey(a.Sym.Feats) < featsKey(b.Sym.Feats)
	})
	return out
}

// item is an active chart edge: a partially matched rule expansion with its
// own private substitution.
type item struct {
	rule  int
	lhs   Symbol
	rhs   []Symbol
	dot   int
	start int
	end   int
	s     subst
	kids  []*Node
}

type parser struct {
	g       *Grammar
	words   []string
	ren     *renamer
	items   map[string]struct{}
	nodes   map[string]*Node
	terms   map[int]*Node
	order   []*Node
	byStart map[int][]*Node
	waiting map[int][]*item
	agenda  []any
}

// Parse runs the chart parser and returns the shared forest of all
// unifiable analyses of words under g.
func Parse(g *Grammar, words []string) (*Forest, error) {
	if err := g.validate(); err != nil {
		return nil, err
	}
	if len(words) == 0 {
		return nil, fmt.Errorf("uniparse: empty input")
	}
	if len(words) > MaxWords {
		return nil, fmt.Errorf("uniparse: input has %d words, max is %d", len(words), MaxWords)
	}
	p := &parser{
		g:       g,
		words:   words,
		ren:     &renamer{},
		items:   map[string]struct{}{},
		nodes:   map[string]*Node{},
		terms:   map[int]*Node{},
		byStart: map[int][]*Node{},
		waiting: map[int][]*item{},
	}
	for ri := range g.Rules {
		for pos := 0; pos <= len(words); pos++ {
			p.seed(ri, pos)
		}
	}
	for len(p.agenda) > 0 {
		e := p.agenda[len(p.agenda)-1]
		p.agenda = p.agenda[:len(p.agenda)-1]
		switch e := e.(type) {
		case *item:
			p.processItem(e)
		case *Node:
			p.processNode(e)
		}
	}

	f := &Forest{Words: words, nodes: p.order}
	start := g.start()
	for _, n := range p.order {
		if n.Start != 0 || n.End != len(words) || n.Sym.Name != start.Name {
			continue
		}
		if len(start.Feats) > 0 {
			nf := instFeats(n.Sym.Feats, p.ren, map[string]Value{})
			if _, ok := unifyFeats(subst{}, start.Feats, nf); !ok {
				continue
			}
		}
		f.Roots = append(f.Roots, n)
	}
	return f, nil
}

// seed creates a fresh instance of rule ri at position pos. Every variable
// in the rule is renamed fresh, so expansions never share variables.
func (p *parser) seed(ri, pos int) {
	r := &p.g.Rules[ri]
	m := map[string]Value{}
	lhs := r.LHS
	lhs.Feats = instFeats(r.LHS.Feats, p.ren, m)
	rhs := make([]Symbol, len(r.RHS))
	for i, s := range r.RHS {
		rhs[i] = s
		if !s.Terminal {
			rhs[i].Feats = instFeats(s.Feats, p.ren, m)
		}
	}
	p.addItem(&item{rule: ri, lhs: lhs, rhs: rhs, start: pos, end: pos, s: subst{}})
}

func (p *parser) addItem(it *item) {
	key := p.itemKey(it)
	if _, dup := p.items[key]; dup {
		return
	}
	p.items[key] = struct{}{}
	p.agenda = append(p.agenda, it)
}

func (p *parser) processItem(it *item) {
	if it.dot == len(it.rhs) {
		p.complete(it)
		return
	}
	sym := it.rhs[it.dot]
	if sym.Terminal {
		if it.end < len(p.words) && p.words[it.end] == sym.Name {
			p.advance(it, p.terminal(it.end), it.end+1, it.s)
		}
		return
	}
	p.waiting[it.end] = append(p.waiting[it.end], it)
	for _, n := range p.byStart[it.end] {
		p.combine(it, n)
	}
}

func (p *parser) processNode(n *Node) {
	for _, it := range p.waiting[n.Start] {
		p.combine(it, n)
	}
}

// combine matches a completed node against the next symbol of a waiting
// item. The node's variables are renamed fresh before unification, and the
// item's substitution is copied on success, so branches stay independent.
func (p *parser) combine(it *item, n *Node) {
	sym := it.rhs[it.dot]
	if n.Sym.Terminal || n.Sym.Name != sym.Name {
		return
	}
	nf := instFeats(n.Sym.Feats, p.ren, map[string]Value{})
	s2, ok := unifyFeats(it.s, sym.Feats, nf)
	if !ok {
		return
	}
	p.advance(it, n, n.End, s2)
}

func (p *parser) advance(it *item, kid *Node, end int, s subst) {
	kids := make([]*Node, len(it.kids)+1)
	copy(kids, it.kids)
	kids[len(it.kids)] = kid
	p.addItem(&item{
		rule:  it.rule,
		lhs:   it.lhs,
		rhs:   it.rhs,
		dot:   it.dot + 1,
		start: it.start,
		end:   end,
		s:     s,
		kids:  kids,
	})
}

// complete turns a finished item into a forest node (or a new packing of an
// existing node). The node's features are canonicalized, so derivations
// differing only in variable names share one node.
func (p *parser) complete(it *item) {
	cf := canonicalSeq(it.s, it.lhs.Feats)[0]
	key := nodeKey(it.lhs.Name, cf, it.start, it.end)
	n, ok := p.nodes[key]
	if !ok {
		n = &Node{
			Sym:      NT(it.lhs.Name, cf),
			Start:    it.start,
			End:      it.end,
			id:       len(p.order),
			packKeys: map[string]bool{},
		}
		p.nodes[key] = n
		p.order = append(p.order, n)
		p.byStart[n.Start] = append(p.byStart[n.Start], n)
		p.agenda = append(p.agenda, n)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d", it.rule)
	for _, k := range it.kids {
		fmt.Fprintf(&b, "|%d", k.id)
	}
	if pk := b.String(); !n.packKeys[pk] {
		n.packKeys[pk] = true
		n.Packings = append(n.Packings, &Packing{Rule: &p.g.Rules[it.rule], Children: it.kids})
	}
}

func (p *parser) terminal(pos int) *Node {
	if n, ok := p.terms[pos]; ok {
		return n
	}
	n := &Node{Sym: T(p.words[pos]), Start: pos, End: pos + 1, id: -1}
	p.terms[pos] = n
	return n
}

// itemKey identifies an item up to alpha-equivalence: two items with the
// same key behave identically in every continuation, so keeping one is
// sound and guarantees termination even for recursive grammars.
func (p *parser) itemKey(it *item) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d|%d|%d|%d", it.rule, it.dot, it.start, it.end)
	for _, k := range it.kids {
		fmt.Fprintf(&b, "|%d", k.id)
	}
	maps := make([]Feats, 0, len(it.rhs)+1)
	maps = append(maps, it.lhs.Feats)
	for _, s := range it.rhs {
		maps = append(maps, s.Feats)
	}
	for _, f := range canonicalSeq(it.s, maps...) {
		b.WriteString(featsKey(f))
	}
	return b.String()
}

func nodeKey(name string, f Feats, start, end int) string {
	return fmt.Sprintf("%s%s|%d|%d", name, featsKey(f), start, end)
}

package main

import (
	"fmt"
	"strings"
)

// Tree is one concrete parse tree. Leaves carry the matched token.
type Tree struct {
	Symbol   string
	Token    string
	Children []*Tree
}

// String renders the tree as an S-expression, e.g. (E (E id[a]) + (E id[b])).
func (t *Tree) String() string {
	if len(t.Children) == 0 {
		if t.Token != "" && t.Token != t.Symbol {
			return t.Symbol + "[" + t.Token + "]"
		}
		return t.Symbol
	}
	parts := make([]string, len(t.Children))
	for i, ch := range t.Children {
		parts[i] = ch.String()
	}
	return "(" + t.Symbol + " " + strings.Join(parts, " ") + ")"
}

// Pretty renders the tree as an indented outline.
func (t *Tree) Pretty() string {
	var b strings.Builder
	t.pretty(&b, 0)
	return b.String()
}

func (t *Tree) pretty(b *strings.Builder, depth int) {
	b.WriteString(strings.Repeat("  ", depth))
	if len(t.Children) == 0 {
		fmt.Fprintf(b, "%s %q\n", t.Symbol, t.Token)
		return
	}
	b.WriteString(t.Symbol)
	b.WriteString("\n")
	for _, ch := range t.Children {
		ch.pretty(b, depth+1)
	}
}

// itemSet identifies an item living in a particular chart set.
type itemSet struct {
	it  Item
	set int
}

// forest enumerates parse trees from a chart by walking back-pointers.
type forest struct {
	c          *Chart
	g          *Grammar
	treeMemo   map[itemSet][]*Tree
	partMemo   map[itemSet][][]*Tree
	inProgress map[itemSet]bool
}

// Trees enumerates every parse tree of a successful parse. For ambiguous
// grammars all legal trees are returned. Grammars with derivation cycles
// (e.g. A -> A) have infinitely many trees; only the finite, acyclic
// derivations are enumerated.
func (c *Chart) Trees() []*Tree {
	if !c.Accepted() {
		return nil
	}
	f := &forest{
		c:          c,
		g:          c.g,
		treeMemo:   make(map[itemSet][]*Tree),
		partMemo:   make(map[itemSet][][]*Tree),
		inProgress: make(map[itemSet]bool),
	}
	var out []*Tree
	for _, t := range f.trees(itemSet{it: c.accept, set: len(c.toks)}) {
		out = append(out, t.Children[0]) // unwrap S' -> Start
	}
	return out
}

// trees returns all trees rooted at the LHS of a completed item.
func (f *forest) trees(key itemSet) []*Tree {
	if ts, ok := f.treeMemo[key]; ok {
		return ts
	}
	rule := f.g.Rules[key.it.Rule]
	var out []*Tree
	for _, children := range f.partials(key) {
		out = append(out, &Tree{Symbol: rule.LHS, Children: children})
	}
	f.treeMemo[key] = out
	return out
}

// partials returns, for an item (rule, dot) ending at chart set `set`,
// every child sequence covering rule.RHS[:dot].
func (f *forest) partials(key itemSet) [][]*Tree {
	if ps, ok := f.partMemo[key]; ok {
		return ps
	}
	if f.inProgress[key] {
		return nil // derivation cycle: no finite tree along this path
	}
	f.inProgress[key] = true
	defer delete(f.inProgress, key)

	it := key.it
	if it.Dot == 0 {
		f.partMemo[key] = [][]*Tree{{}}
		return f.partMemo[key]
	}
	rule := f.g.Rules[it.Rule]
	var out [][]*Tree
	for _, bp := range f.c.sets[key.set].backs[it] {
		if bp.isScan {
			term := rule.RHS[it.Dot-1]
			leaf := &Tree{Symbol: term, Token: bp.terminal}
			for _, pre := range f.partials(itemSet{it: bp.prev, set: bp.prevSet}) {
				out = append(out, append(append([]*Tree{}, pre...), leaf))
			}
		} else {
			pres := f.partials(itemSet{it: bp.prev, set: bp.prevSet})
			subs := f.trees(itemSet{it: bp.completed, set: key.set})
			for _, pre := range pres {
				for _, sub := range subs {
					out = append(out, append(append([]*Tree{}, pre...), sub))
				}
			}
		}
	}
	f.partMemo[key] = out
	return out
}

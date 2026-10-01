package main

import "sort"

// ConflictNode is one output label set that more than one input series maps
// to. Node identity is the output LabelSet content, not any rewritten text.
type ConflictNode struct {
	Output LabelSet
	Inputs []string // sorted input series keys
	Rules  []string // IDs of rules that changed any of these inputs
}

// ConflictGraph maps canonical output keys to conflict nodes. It supports
// combining multiple rules and dynamic retraction: the graph is rebuilt from
// the stored inputs and the remaining rules whenever rules change.
type ConflictGraph struct {
	inputs []Series
	rules  []Rule
	Nodes  map[string]*ConflictNode
}

// BuildConflictGraph constructs the graph for inputs under the given ordered
// rules. Only collisions (>= 2 distinct inputs on one output) become nodes.
func BuildConflictGraph(inputs []Series, rules []Rule) *ConflictGraph {
	g := &ConflictGraph{inputs: append([]Series(nil), inputs...), rules: append([]Rule(nil), rules...)}
	g.rebuild()
	return g
}

// Retract dynamically removes a rule and rebuilds the graph.
func (g *ConflictGraph) Retract(ruleID string) {
	var kept []Rule
	for _, r := range g.rules {
		if r.ID() != ruleID {
			kept = append(kept, r)
		}
	}
	g.rules = kept
	g.rebuild()
}

// HasConflicts reports whether any collision exists.
func (g *ConflictGraph) HasConflicts() bool { return len(g.Nodes) > 0 }

// MinimalPair returns the lexicographically smallest conflicting input pair
// across the whole graph, suitable as the witness for a rejected batch.
func (g *ConflictGraph) MinimalPair() ([2]string, bool) {
	var best [2]string
	found := false
	keys := make([]string, 0, len(g.Nodes))
	for k := range g.Nodes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		n := g.Nodes[k]
		pair := [2]string{n.Inputs[0], n.Inputs[1]}
		if !found || pair[0] < best[0] || (pair[0] == best[0] && pair[1] < best[1]) {
			best, found = pair, true
		}
	}
	return best, found
}

func (g *ConflictGraph) rebuild() {
	g.Nodes = map[string]*ConflictNode{}
	type acc struct {
		out     LabelSet
		inputs  []string
		changed map[string]bool
	}
	groups := map[string]*acc{}
	for _, s := range g.inputs {
		out := s.Labels
		changed := map[string]bool{}
		for _, r := range g.rules {
			next := r.Apply(out)
			if next.Key() != out.Key() {
				changed[r.ID()] = true
			}
			out = next
		}
		k := out.Key()
		a, ok := groups[k]
		if !ok {
			a = &acc{out: out, changed: map[string]bool{}}
			groups[k] = a
		}
		a.inputs = append(a.inputs, s.Key())
		for id := range changed {
			a.changed[id] = true
		}
	}
	for k, a := range groups {
		if len(a.inputs) < 2 {
			continue
		}
		sort.Strings(a.inputs)
		var ruleIDs []string
		for id := range a.changed {
			ruleIDs = append(ruleIDs, id)
		}
		sort.Strings(ruleIDs)
		g.Nodes[k] = &ConflictNode{Output: a.out, Inputs: a.inputs, Rules: ruleIDs}
	}
}

// EnumerateDomain expands a small label domain (label -> possible values)
// into the full set of input series, used to verify the conflict scope
// exhaustively over a bounded space.
func EnumerateDomain(name string, domain map[string][]string) []Series {
	names := make([]string, 0, len(domain))
	for n := range domain {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []Series
	var rec func(i int, ls LabelSet)
	rec = func(i int, ls LabelSet) {
		if i == len(names) {
			out = append(out, Series{Name: name, Labels: ls.Clone()})
			return
		}
		for _, v := range domain[names[i]] {
			ls[names[i]] = v
			rec(i+1, ls)
		}
		delete(ls, names[i])
	}
	rec(0, LabelSet{})
	return out
}

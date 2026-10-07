// Package bisim computes the coarsest bisimulation partition of a finite
// automaton whose states carry output labels and whose transitions are
// labeled and may be multi-edges (several edges with the same label
// between the same pair of states).
//
// Two states are bisimilar iff they carry the same output label and
// every labeled move of one can be matched by a move with the same label
// of the other into bisimilar successors. In particular a missing
// transition is NOT the same as a transition into a rejecting state, and
// states are never merged merely because they are both accepting or both
// reachable.
package bisim

import (
	"fmt"
	"sort"
)

// Automaton is a finite, edge-labeled multigraph with an output label on
// every state. Edge labels are arbitrary strings; duplicate edges
// (multi-edges) are preserved but, per standard bisimulation semantics,
// do not change the resulting partition.
type Automaton struct {
	outputs []string
	edges   []Edge
	// adj[from][label] = deduplicated, sorted list of targets.
	adj []map[string][]int
}

// Edge is a single labeled transition from From to To.
type Edge struct {
	From  int
	Label string
	To    int
}

// New builds an automaton with n states. outputs[i] is the output label
// of state i (e.g. "accept" / "reject"); it panics if len(outputs) != n.
func New(n int, outputs []string) *Automaton {
	if len(outputs) != n {
		panic(fmt.Sprintf("bisim: %d states but %d outputs", n, len(outputs)))
	}
	a := &Automaton{
		outputs: append([]string(nil), outputs...),
		adj:     make([]map[string][]int, n),
	}
	for i := range a.adj {
		a.adj[i] = make(map[string][]int)
	}
	return a
}

// AddEdge adds a labeled transition. Multi-edges are allowed.
func (a *Automaton) AddEdge(from int, label string, to int) {
	n := len(a.outputs)
	if from < 0 || from >= n || to < 0 || to >= n {
		panic(fmt.Sprintf("bisim: edge %d -%s-> %d out of range [0,%d)", from, label, to, n))
	}
	a.edges = append(a.edges, Edge{From: from, Label: label, To: to})
	targets := a.adj[from][label]
	i := sort.SearchInts(targets, to)
	if i >= len(targets) || targets[i] != to {
		targets = append(targets, 0)
		copy(targets[i+1:], targets[i:])
		targets[i] = to
		a.adj[from][label] = targets
	}
}

// NumStates returns the number of states.
func (a *Automaton) NumStates() int { return len(a.outputs) }

// Output returns the output label of state s.
func (a *Automaton) Output(s int) string { return a.outputs[s] }

// Transitions returns the deduplicated, sorted targets of state s for
// the given edge label, or nil if the transition is missing.
func (a *Automaton) Transitions(s int, label string) []int {
	return a.adj[s][label]
}

// Labels returns the sorted set of edge labels used anywhere.
func (a *Automaton) Labels() []string {
	seen := make(map[string]bool)
	var labels []string
	for _, e := range a.edges {
		if !seen[e.Label] {
			seen[e.Label] = true
			labels = append(labels, e.Label)
		}
	}
	sort.Strings(labels)
	return labels
}

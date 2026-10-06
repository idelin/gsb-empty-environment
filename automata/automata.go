// Package automata provides finite automata with per-state output labels and
// labeled multi-edges, together with coarsest bisimulation partitioning.
//
// Bisimulation here is defined over the full transition behavior:
//   - states must carry equal output labels;
//   - for every edge label, the multiset of successor blocks must match
//     (edge multiplicity matters);
//   - a missing transition is NOT the same as a transition into a rejecting
//     (or any particular) state: both change the successor multiset and are
//     therefore distinguished.
//
// In particular, states are never merged merely because they can reach the
// same accepting states; the partition is a genuine bisimulation.
package automata

import (
	"fmt"
	"sort"
)

// Edge is a single labeled transition. Parallel edges (same From, Label, To)
// are allowed and are semantically significant: they form a multigraph.
type Edge struct {
	From  int
	Label string
	To    int
}

// Automaton is a finite edge-labeled multigraph whose states carry output
// labels. There is no distinguished start or accepting set baked into the
// semantics; "accepting" is just one possible output label, and rejecting
// states are ordinary states with their own behavior.
type Automaton struct {
	NumStates int
	Outputs   []string // Outputs[s] is the output label of state s.
	Edges     []Edge   // May contain duplicate (parallel) edges.
}

// New builds an Automaton and validates it.
func New(numStates int, outputs []string, edges []Edge) (*Automaton, error) {
	a := &Automaton{NumStates: numStates, Outputs: outputs, Edges: edges}
	return a, a.Validate()
}

// Validate checks structural invariants.
func (a *Automaton) Validate() error {
	if a.NumStates < 0 {
		return fmt.Errorf("automata: negative state count %d", a.NumStates)
	}
	if len(a.Outputs) != a.NumStates {
		return fmt.Errorf("automata: %d outputs for %d states", len(a.Outputs), a.NumStates)
	}
	for i, e := range a.Edges {
		if e.From < 0 || e.From >= a.NumStates || e.To < 0 || e.To >= a.NumStates {
			return fmt.Errorf("automata: edge %d (%d --%s--> %d) out of range [0,%d)",
				i, e.From, e.Label, e.To, a.NumStates)
		}
	}
	return nil
}

// Labels returns the sorted set of edge labels in use.
func (a *Automaton) Labels() []string {
	seen := make(map[string]bool)
	for _, e := range a.Edges {
		seen[e.Label] = true
	}
	labels := make([]string, 0, len(seen))
	for l := range seen {
		labels = append(labels, l)
	}
	sort.Strings(labels)
	return labels
}

// succIndex builds from -> label -> to -> multiplicity adjacency.
func (a *Automaton) succIndex() []map[string]map[int]int {
	succ := make([]map[string]map[int]int, a.NumStates)
	for s := 0; s < a.NumStates; s++ {
		succ[s] = make(map[string]map[int]int)
	}
	for _, e := range a.Edges {
		m := succ[e.From][e.Label]
		if m == nil {
			m = make(map[int]int)
			succ[e.From][e.Label] = m
		}
		m[e.To]++
	}
	return succ
}

package bisim

import "fmt"

// BisimulationRelation computes the coarsest bisimulation as an explicit
// binary relation, using the textbook direct fixed-point iteration:
//
//	R_0        = { (s,t) | output(s) = output(t) }
//	R_{i+1}    = { (s,t) in R_i | every move of s is matched by t
//	               (same label, R_i-related target) and vice versa }
//	R*         = the greatest fixed point
//
// It is intentionally naive (O(n^2) per iteration over all labels) and
// serves as an independent model to cross-check the partition-refinement
// implementation.
func (a *Automaton) BisimulationRelation() [][]bool {
	n := len(a.outputs)
	rel := make([][]bool, n)
	for i := range rel {
		rel[i] = make([]bool, n)
		for j := range rel[i] {
			rel[i][j] = a.outputs[i] == a.outputs[j]
		}
	}
	labels := a.Labels()
	for changed := true; changed; {
		changed = false
		for s := 0; s < n; s++ {
			for t := 0; t < n; t++ {
				if !rel[s][t] {
					continue
				}
				if !movesMatch(a, rel, labels, s, t) || !movesMatch(a, rel, labels, t, s) {
					rel[s][t] = false
					changed = true
				}
			}
		}
	}
	return rel
}

// movesMatch reports whether every labeled move of s can be answered by
// some move of t with the same label into an R-related target.
func movesMatch(a *Automaton, rel [][]bool, labels []string, s, t int) bool {
	for _, l := range labels {
		for _, sp := range a.adj[s][l] {
			matched := false
			for _, tp := range a.adj[t][l] {
				if rel[sp][tp] {
					matched = true
					break
				}
			}
			if !matched {
				return false
			}
		}
	}
	return true
}

// CheckQuotient verifies a quotient against the direct fixed-point
// relation model. It returns an error describing the first discrepancy:
//   - the block map must induce exactly the fixed-point relation;
//   - every block must have a single, correct output label;
//   - every quotient edge must be witnessed by a concrete transition,
//     and every concrete transition must appear in the quotient.
func (a *Automaton) CheckQuotient(q *Quotient) error {
	n := len(a.outputs)
	if len(q.BlockOf) != n {
		return fmt.Errorf("bisim: BlockOf has %d entries, want %d", len(q.BlockOf), n)
	}
	rel := a.BisimulationRelation()
	for s := 0; s < n; s++ {
		if q.BlockOf[s] < 0 || q.BlockOf[s] >= q.NumBlocks {
			return fmt.Errorf("bisim: state %d mapped to invalid block %d", s, q.BlockOf[s])
		}
		for t := 0; t < n; t++ {
			if (q.BlockOf[s] == q.BlockOf[t]) != rel[s][t] {
				return fmt.Errorf("bisim: states %d,%d: partition says same-block=%v, fixed-point relation says %v",
					s, t, q.BlockOf[s] == q.BlockOf[t], rel[s][t])
			}
		}
		if q.Output[q.BlockOf[s]] != a.outputs[s] {
			return fmt.Errorf("bisim: block %d output %q, state %d has %q",
				q.BlockOf[s], q.Output[q.BlockOf[s]], s, a.outputs[s])
		}
	}
	// Quotient edges must be exactly the image of the concrete edges.
	type edgeKey struct {
		from, label, to string
	}
	want := make(map[[3]string]bool)
	for s := 0; s < n; s++ {
		for _, l := range a.Labels() {
			for _, t := range a.adj[s][l] {
				want[[3]string{fmt.Sprint(q.BlockOf[s]), l, fmt.Sprint(q.BlockOf[t])}] = true
			}
		}
	}
	got := make(map[[3]string]bool)
	for b := 0; b < q.NumBlocks; b++ {
		for l, targets := range q.Edges[b] {
			for _, tb := range targets {
				k := [3]string{fmt.Sprint(b), l, fmt.Sprint(tb)}
				got[k] = true
				if !want[k] {
					return fmt.Errorf("bisim: quotient edge %d -%s-> %d has no concrete witness", b, l, tb)
				}
			}
		}
	}
	for k := range want {
		if !got[k] {
			return fmt.Errorf("bisim: concrete edge projects to quotient edge %s -%s-> %s, missing from quotient", k[0], k[1], k[2])
		}
	}
	return nil
}

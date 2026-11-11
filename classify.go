package netsec

import "fmt"

// Relation is the classification of a hypothesised association between two
// events under every clock offset consistent with the known intervals.
type Relation uint8

const (
	// Impossible means no feasible offset assignment allows the relation.
	Impossible Relation = iota
	// Possible means some feasible offsets allow the relation and some do not.
	Possible
	// Necessary means every feasible offset assignment forces the relation.
	Necessary
)

func (r Relation) String() string {
	switch r {
	case Impossible:
		return "impossible"
	case Possible:
		return "possible"
	case Necessary:
		return "necessary"
	}
	return "unknown"
}

// NegationEvidence explains why a Necessary relation cannot be violated:
// assuming the events are out of order, or further apart than the gap, each
// yields a negative cycle.
type NegationEvidence struct {
	OutOfOrder  *CycleEvidence // contradiction from global[J] < global[I]
	GapExceeded *CycleEvidence // contradiction from global[J]-global[I] > maxGap
}

// Verdict is the outcome of Classify.
type Verdict struct {
	Relation Relation
	Witness  *Solution         // feasible offsets, set for Possible and Necessary
	Evidence *CycleEvidence    // negative cycle, set for Impossible
	Negation *NegationEvidence // why the relation must hold, set for Necessary
}

// Classify determines whether event J can follow event I within maxGap
// milliseconds, i.e. whether 0 <= global[J]-global[I] <= maxGap, given every
// clock offset consistent with the probe intervals and the constraints.
//
// Because times are integer milliseconds, the negation of the relation is the
// disjunction global[J]-global[I] <= -1 or >= maxGap+1; the relation is
// necessary exactly when both disjuncts are infeasible.
func (s *System) Classify(i, j int, maxGap int64) (Verdict, error) {
	n := len(s.events)
	if i < 0 || i >= n || j < 0 || j >= n {
		return Verdict{}, fmt.Errorf("netsec: classify references unknown event")
	}
	if maxGap < 0 {
		return Verdict{}, fmt.Errorf("netsec: negative max gap")
	}

	// Hypothesis: I precedes J by at most maxGap.
	hypothesis := []edge{
		{j, i, 0, fmt.Sprintf("hypothesis: g[%d] <= g[%d]", i, j)},
		{i, j, maxGap, fmt.Sprintf("hypothesis: g[%d] - g[%d] <= %d", j, i, maxGap)},
	}
	numVars, edges := s.build(hypothesis)
	dist, cycle := bellmanFord(numVars, edges)
	if cycle != nil {
		return Verdict{Relation: Impossible, Evidence: evidenceFrom(cycle)}, nil
	}
	witness := solutionFrom(s, dist)

	// Negation, disjunct 1: J strictly before I.
	_, e1 := s.build([]edge{{i, j, -1, fmt.Sprintf("negation: g[%d] - g[%d] <= -1", j, i)}})
	_, cycleOrder := bellmanFord(numVars, e1)

	// Negation, disjunct 2: J more than maxGap after I.
	_, e2 := s.build([]edge{{j, i, -(maxGap + 1), fmt.Sprintf("negation: g[%d] - g[%d] >= %d", j, i, maxGap+1)}})
	_, cycleGap := bellmanFord(numVars, e2)

	if cycleOrder != nil && cycleGap != nil {
		return Verdict{
			Relation: Necessary,
			Witness:  witness,
			Negation: &NegationEvidence{
				OutOfOrder:  evidenceFrom(cycleOrder),
				GapExceeded: evidenceFrom(cycleGap),
			},
		}, nil
	}
	return Verdict{Relation: Possible, Witness: witness}, nil
}

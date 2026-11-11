package wafdecision

import "math"

// VerifyReport is the result of an exhaustive verification pass.
type VerifyReport struct {
	// Assignments is the number of truth assignments checked (2^LeafCount).
	Assignments int
	// MaxObservedCost is the largest total cost actually observed.
	MaxObservedCost uint64
	// MinObservedCost is the smallest total cost actually observed.
	MinObservedCost uint64
	// MaxCalls is the largest number of detector invocations in one request;
	// the at-most-once contract requires MaxCalls <= LeafCount.
	MaxCalls int
	// ExpectedCostLo is the smallest expected total cost over the selectivity
	// interval box (independent leaves).
	ExpectedCostLo float64
	// ExpectedCostHi is the largest expected total cost over the selectivity
	// interval box.
	ExpectedCostHi float64
	// ExpectedCostMid is the expected total cost at interval midpoints.
	ExpectedCostMid float64
}

// OK reports whether verification produced a report. A non-nil report means
// every semantic and cost-bound check passed; failures return an error.
func (r *VerifyReport) OK() bool {
	return r != nil
}

// Verify exhaustively checks the compiled graph against all truth
// assignments of its leaves. For every assignment it confirms that:
//
//   - the graph verdict matches the original formula semantics;
//   - no leaf detector is invoked more than once;
//   - the accumulated cost never exceeds the compiler's worst-cost bound.
//
// It then evaluates expected costs at every selectivity interval vertex to
// obtain robust expected-cost bounds. With at most 12 leaves this covers at
// most 4,096 truth assignments and 4,096 box vertices.
func (g *Graph) Verify() (*VerifyReport, error) {
	if g == nil || len(g.nodes) == 0 {
		return nil, &VerifyError{Reason: "nil or empty graph"}
	}
	n := g.LeafCount
	total := 1 << uint(n)
	costs := make([]uint64, total)
	maxCalls := 0

	rep := &VerifyReport{
		Assignments:     total,
		MinObservedCost: math.MaxUint64,
	}

	for mask := 0; mask < total; mask++ {
		assignment := make(map[int]bool, n)
		detectors := make(map[int]Detector, n)
		calls := make(map[int]int, n)
		for i, s := range g.specs {
			value := mask&(1<<uint(i)) != 0
			assignment[s.ID] = value
			detectors[s.ID] = detectorStub(assignment, calls, s.ID)
		}

		want, err := EvalFormula(g.formula, assignment)
		if err != nil {
			return nil, err
		}
		verdict, st := g.Eval(detectors)
		if (verdict == Deny) != want {
			return nil, &VerifyError{Reason: "verdict mismatch"}
		}
		for _, count := range calls {
			if count > 1 {
				return nil, &VerifyError{Reason: "leaf evaluated more than once"}
			}
		}
		if st.CallCount > n {
			return nil, &VerifyError{Reason: "invocation count exceeds leaf count"}
		}
		if st.TotalCost > g.WorstCost {
			return nil, &VerifyError{Reason: "cost exceeds compiled worst-case bound"}
		}
		if len(st.Tested) != st.CallCount {
			return nil, &VerifyError{Reason: "tested set disagrees with call count"}
		}

		costs[mask] = st.TotalCost
		if st.CallCount > maxCalls {
			maxCalls = st.CallCount
		}
		if st.TotalCost > rep.MaxObservedCost {
			rep.MaxObservedCost = st.TotalCost
		}
		if st.TotalCost < rep.MinObservedCost {
			rep.MinObservedCost = st.TotalCost
		}
	}

	rep.MaxCalls = maxCalls
	rep.ExpectedCostLo, rep.ExpectedCostHi = g.expectedCostBox(costs)
	rep.ExpectedCostMid = g.expectedCostAt(costs, g.midpointProbs())

	if rep.MaxObservedCost != g.WorstCost {
		return nil, &VerifyError{Reason: "bound not tight: observed worst cost differs from compiler bound"}
	}
	return rep, nil
}

func detectorStub(assignment map[int]bool, calls map[int]int, leafID int) Detector {
	return func() bool {
		calls[leafID]++
		return assignment[leafID]
	}
}

func (g *Graph) midpointProbs() []float64 {
	probs := make([]float64, len(g.specs))
	for i, s := range g.specs {
		probs[i] = s.SelMid()
	}
	return probs
}

// assignmentProbs fills prob[mask] with the joint probability of each full
// truth assignment for independent Bernoulli leaves, using an O(n*2^n)
// recurrence: prob[mask|bit] = prob[mask] * p/(1-p).
func (g *Graph) assignmentProbs(probs []float64) []float64 {
	n := len(g.specs)
	prob := make([]float64, 1<<uint(n))
	prob[0] = 1
	for i := 0; i < n; i++ {
		p := probs[i]
		bit := 1 << uint(i)
		prev := 1 << uint(i)
		for mask := 0; mask < prev; mask++ {
			base := prob[mask]
			prob[mask] = base * (1 - p)
			if 1-p != 0 {
				prob[mask|bit] = base * p
			} else {
				prob[mask|bit] = base // p == 1: branch false has zero weight
			}
		}
	}
	return prob
}

// expectedCostAt computes E[cost] for independent Bernoulli leaves with the
// given per-leaf true probabilities.
func (g *Graph) expectedCostAt(costs []uint64, probs []float64) float64 {
	prob := g.assignmentProbs(probs)
	var expected float64
	for mask, c := range costs {
		expected += float64(c) * prob[mask]
	}
	return expected
}

// expectedCostBox finds the extrema of the expected cost over the selectivity
// interval box. The box is multi-affine in the per-leaf endpoint variables, so
// every extremum is attained at a vertex pinning each leaf probability to
// SelLo or SelHi. At most 2^MaxLeaves = 4096 vertices are enumerated; each
// vertex expectation is computed in O(n*2^n).
func (g *Graph) expectedCostBox(costs []uint64) (lo, hi float64) {
	n := len(g.specs)
	lo, hi = math.MaxFloat64, -math.MaxFloat64
	vertices := 1 << uint(n)
	probs := make([]float64, n)
	for vertex := 0; vertex < vertices; vertex++ {
		for i, s := range g.specs {
			if vertex&(1<<uint(i)) != 0 {
				probs[i] = s.SelHi
			} else {
				probs[i] = s.SelLo
			}
		}
		expected := g.expectedCostAt(costs, probs)
		lo = math.Min(lo, expected)
		hi = math.Max(hi, expected)
	}
	if math.IsNaN(lo) || math.IsNaN(hi) {
		return 0, 0
	}
	return lo, hi
}

package psdcomp

import (
	"fmt"
	"math/big"
)

// PruneReason identifies why a search branch was cut.
type PruneReason int

const (
	// PruneMinorViolation: a fully determined principal submatrix of the
	// partial assignment has a negative determinant. Since every
	// principal submatrix of a PSD matrix is PSD, no extension of this
	// prefix can be feasible.
	PruneMinorViolation PruneReason = iota
	// PruneTraceBound: the partial trace plus the minimal achievable
	// trace of the remaining diagonal variables already exceeds the
	// best known trace, so this branch cannot improve the optimum.
	PruneTraceBound
)

func (r PruneReason) String() string {
	switch r {
	case PruneMinorViolation:
		return "minor_violation"
	case PruneTraceBound:
		return "trace_bound"
	}
	return "unknown"
}

// Assignment records one decided variable in a search prefix.
type Assignment struct {
	I, J  int
	Value *big.Rat
}

// PruneEvent is the per-branch pruning evidence: where in the search the
// branch was cut, which prefix led there, and the exact witness.
type PruneEvent struct {
	Depth  int
	Prefix []Assignment
	Reason PruneReason
	// Minor is set for PruneMinorViolation: the negative principal minor.
	Minor *MinorReport
	// Bound and Best are set for PruneTraceBound.
	Bound *big.Rat
	Best  *big.Rat
}

// Stats aggregates search statistics.
type Stats struct {
	Combos      uint64 // total candidate combinations
	Nodes       uint64 // candidate assignments tried
	Leaves      uint64 // complete combinations evaluated
	PrunedMinor uint64 // branches cut by a negative principal minor
	PrunedTrace uint64 // branches cut by the trace lower bound
}

// InfeasibleInfo explains why no PSD completion exists.
type InfeasibleInfo struct {
	Reason string
	// Minors lists fully fixed principal minors that are already
	// negative before any variable is assigned.
	Minors []MinorReport
}

// Result is the outcome of a Solve call.
type Result struct {
	Feasible bool
	Matrix   *Matrix
	Trace    *big.Rat
	// Certificate lists every principal minor of Matrix (all >= 0),
	// computed with DetBareiss.
	Certificate []MinorReport
	Stats       Stats
	Infeasible  *InfeasibleInfo
}

// Solver searches for the optimal PSD completion. The zero value is
// ready to use.
type Solver struct {
	// OnPrune, if non-nil, is invoked for every pruned branch with the
	// full evidence. Enabling it on large searches can be expensive.
	OnPrune func(PruneEvent)
}

// minorPlan describes one principal minor to evaluate during the search:
// the ordered-variable indices inside its row/column set.
type minorPlan struct {
	indices []int // matrix indices of the principal submatrix
	vars    []int // ordered variable indices occurring inside it
	maxVar  int   // max of vars, -1 if none
}

// Solve finds a PSD completion minimizing the trace, breaking ties
// lexicographically on the upper triangle. It returns an error only for
// malformed problems; infeasibility is reported in Result.Infeasible.
func (s *Solver) Solve(p *Problem) (*Result, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	idx, cands := p.orderedVars()
	nv := len(idx)
	base := p.baseMatrix()

	res := &Result{}
	res.Stats.Combos = p.TotalCombos()

	// Partition principal minors into fully fixed ones (checked once at
	// the root) and ones completed when their last variable is assigned.
	plans := make([]minorPlan, 0)
	for _, subset := range IndexSubsets(p.N) {
		mp := minorPlan{indices: subset, maxVar: -1}
		inS := make(map[int]bool, len(subset))
		for _, r := range subset {
			inS[r] = true
		}
		for k, vi := range idx {
			v := p.Vars[vi]
			if inS[v.I] && inS[v.J] {
				mp.vars = append(mp.vars, k)
				if k > mp.maxVar {
					mp.maxVar = k
				}
			}
		}
		plans = append(plans, mp)
	}
	byDepth := make([][]int, nv+1) // minor plan indices keyed by maxVar+1
	var rootPlans []int
	for pi, mp := range plans {
		if mp.maxVar < 0 {
			rootPlans = append(rootPlans, pi)
		} else {
			byDepth[mp.maxVar+1] = append(byDepth[mp.maxVar+1], pi)
		}
	}

	// Root check: fully fixed principal minors must already be >= 0.
	var bad []MinorReport
	for _, pi := range rootPlans {
		rep := MinorReport{Indices: plans[pi].indices, Det: DetBareiss(base.Submatrix(plans[pi].indices))}
		if rep.Det.Sign() < 0 {
			bad = append(bad, rep)
		}
	}
	if len(bad) > 0 {
		res.Infeasible = &InfeasibleInfo{
			Reason: "fixed entries already violate a principal minor; no completion can be PSD",
			Minors: bad,
		}
		return res, nil
	}

	// Trace bookkeeping: fixed diagonal sum plus suffix minima of the
	// remaining diagonal variables.
	fixedTrace := new(big.Rat)
	for _, f := range p.Fixed {
		if f.I == f.J {
			fixedTrace.Add(fixedTrace, f.Value)
		}
	}
	suffixMin := make([]*big.Rat, nv+1)
	suffixMin[nv] = new(big.Rat)
	for k := nv - 1; k >= 0; k-- {
		suffixMin[k] = new(big.Rat).Set(suffixMin[k+1])
		v := p.Vars[idx[k]]
		if v.I == v.J {
			suffixMin[k].Add(suffixMin[k], cands[k][0]) // candidates sorted ascending
		}
	}

	work := base.Clone()
	prefix := make([]Assignment, 0, nv)
	var bestTrace *big.Rat
	var bestMatrix *Matrix

	emit := func(depth int, reason PruneReason, minor *MinorReport, bound *big.Rat) {
		if s.OnPrune == nil {
			return
		}
		ev := PruneEvent{Depth: depth, Reason: reason, Minor: minor}
		ev.Prefix = make([]Assignment, len(prefix))
		for i, a := range prefix {
			ev.Prefix[i] = Assignment{I: a.I, J: a.J, Value: new(big.Rat).Set(a.Value)}
		}
		if reason == PruneTraceBound {
			ev.Bound = new(big.Rat).Set(bound)
			ev.Best = new(big.Rat).Set(bestTrace)
		}
		s.OnPrune(ev)
	}

	var dfs func(depth int, traceSoFar *big.Rat)
	dfs = func(depth int, traceSoFar *big.Rat) {
		if depth == nv {
			res.Stats.Leaves++
			total := new(big.Rat).Add(fixedTrace, traceSoFar)
			if bestTrace == nil || total.Cmp(bestTrace) < 0 ||
				(total.Cmp(bestTrace) == 0 && lexLess(work, bestMatrix)) {
				bestTrace = total
				bestMatrix = work.Clone()
			}
			return
		}
		v := p.Vars[idx[depth]]
		isDiag := v.I == v.J
		for _, c := range cands[depth] {
			res.Stats.Nodes++
			work.Set(v.I, v.J, c)
			newTrace := new(big.Rat).Set(traceSoFar)
			if isDiag {
				newTrace.Add(newTrace, c)
			}
			// Trace lower bound pruning.
			if bestTrace != nil {
				bound := new(big.Rat).Add(fixedTrace, newTrace)
				bound.Add(bound, suffixMin[depth+1])
				if bound.Cmp(bestTrace) > 0 {
					res.Stats.PrunedTrace++
					emit(depth, PruneTraceBound, nil, bound)
					continue
				}
			}
			// Principal minor pruning: minors completed by this
			// assignment must be nonnegative.
			violated := false
			for _, pi := range byDepth[depth+1] {
				mp := plans[pi]
				det := DetBareiss(work.Submatrix(mp.indices))
				if det.Sign() < 0 {
					res.Stats.PrunedMinor++
					rep := MinorReport{Indices: mp.indices, Det: det}
					emit(depth, PruneMinorViolation, &rep, nil)
					violated = true
					break
				}
			}
			if violated {
				continue
			}
			prefix = append(prefix, Assignment{I: v.I, J: v.J, Value: c})
			dfs(depth+1, newTrace)
			prefix = prefix[:len(prefix)-1]
		}
	}
	dfs(0, new(big.Rat))

	if bestMatrix == nil {
		res.Infeasible = &InfeasibleInfo{
			Reason: "no candidate combination yields a PSD matrix",
		}
		return res, nil
	}
	res.Feasible = true
	res.Matrix = bestMatrix
	res.Trace = bestTrace
	res.Certificate = PrincipalMinors(bestMatrix, DetBareiss)
	return res, nil
}

// Solve is a convenience wrapper using a default Solver.
func Solve(p *Problem) (*Result, error) {
	return (&Solver{}).Solve(p)
}

// lexLess reports whether the upper triangle of a is lexicographically
// smaller than that of b.
func lexLess(a, b *Matrix) bool {
	for i := 0; i < a.N; i++ {
		for j := i; j < a.N; j++ {
			switch a.A[i][j].Cmp(b.A[i][j]) {
			case -1:
				return true
			case 1:
				return false
			}
		}
	}
	return false
}

// Describe renders the result certificate in human-readable form.
func (r *Result) Describe() string {
	if !r.Feasible {
		s := "INFEASIBLE: " + r.Infeasible.Reason + "\n"
		for _, m := range r.Infeasible.Minors {
			s += "  " + m.String() + " < 0\n"
		}
		return s
	}
	s := fmt.Sprintf("optimal trace = %s\nmatrix:\n%s", r.Trace.RatString(), r.Matrix.String())
	s += "certificate (all principal minors >= 0):\n"
	for _, c := range r.Certificate {
		s += "  " + c.String() + "\n"
	}
	return s
}

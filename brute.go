package psdcomp

import (
	"fmt"
	"math/big"
)

// BruteForce enumerates every candidate combination without any pruning
// and tests feasibility with IsPSDElimination — an algorithm independent
// of the principal-minor machinery used by Solver. It exists to
// cross-validate Solve and is O(combos) full PSD tests.
func BruteForce(p *Problem) (*Result, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	idx, cands := p.orderedVars()
	nv := len(idx)
	work := p.baseMatrix()

	res := &Result{}
	res.Stats.Combos = p.TotalCombos()

	fixedTrace := new(big.Rat)
	for _, f := range p.Fixed {
		if f.I == f.J {
			fixedTrace.Add(fixedTrace, f.Value)
		}
	}

	var bestTrace *big.Rat
	var bestMatrix *Matrix

	var rec func(depth int, traceSoFar *big.Rat)
	rec = func(depth int, traceSoFar *big.Rat) {
		if depth == nv {
			res.Stats.Leaves++
			res.Stats.Nodes++
			if !IsPSDElimination(work) {
				return
			}
			total := new(big.Rat).Add(fixedTrace, traceSoFar)
			if bestTrace == nil || total.Cmp(bestTrace) < 0 ||
				(total.Cmp(bestTrace) == 0 && lexLess(work, bestMatrix)) {
				bestTrace = total
				bestMatrix = work.Clone()
			}
			return
		}
		v := p.Vars[idx[depth]]
		for _, c := range cands[depth] {
			work.Set(v.I, v.J, c)
			t := new(big.Rat).Set(traceSoFar)
			if v.I == v.J {
				t.Add(t, c)
			}
			rec(depth+1, t)
		}
	}
	rec(0, new(big.Rat))

	if bestMatrix == nil {
		res.Infeasible = &InfeasibleInfo{Reason: "no candidate combination yields a PSD matrix"}
		return res, nil
	}
	res.Feasible = true
	res.Matrix = bestMatrix
	res.Trace = bestTrace
	res.Certificate = PrincipalMinors(bestMatrix, DetGauss)
	return res, nil
}

// VerifyResult independently validates a Result produced by Solve:
//
//  1. the certificate lists all 2^n - 1 principal minors, each >= 0,
//     recomputed with BOTH determinant algorithms (Bareiss and Gauss),
//     which must agree exactly;
//  2. the matrix passes the independent elimination-based PSD test;
//  3. the matrix respects every fixed entry and every variable takes a
//     value from its candidate set;
//  4. the trace equals the sum of diagonal entries;
//  5. a pruning-free BruteForce search over the same problem reaches the
//     same trace and the same optimal matrix.
func VerifyResult(p *Problem, r *Result) error {
	if !r.Feasible {
		bf, err := BruteForce(p)
		if err != nil {
			return err
		}
		if bf.Feasible {
			return fmt.Errorf("psdcomp: Solve reported infeasible but brute force found a solution with trace %s", bf.Trace.RatString())
		}
		return nil
	}
	n := p.N
	m := r.Matrix
	if m == nil || m.N != n {
		return fmt.Errorf("psdcomp: result matrix missing or wrong order")
	}

	// 1. Certificate completeness, nonnegativity, and cross-algorithm
	// agreement.
	want := 1<<n - 1
	if len(r.Certificate) != want {
		return fmt.Errorf("psdcomp: certificate has %d minors, want %d", len(r.Certificate), want)
	}
	gauss := PrincipalMinors(m, DetGauss)
	for i, c := range r.Certificate {
		if c.Det.Sign() < 0 {
			return fmt.Errorf("psdcomp: certificate minor %v is negative: %s", c.Indices, c.Det.RatString())
		}
		if c.Det.Cmp(gauss[i].Det) != 0 {
			return fmt.Errorf("psdcomp: determinant algorithms disagree on minor %v: bareiss=%s gauss=%s",
				c.Indices, c.Det.RatString(), gauss[i].Det.RatString())
		}
	}

	// 2. Independent PSD test.
	if !IsPSDElimination(m) {
		return fmt.Errorf("psdcomp: elimination-based PSD test rejects the result matrix")
	}

	// 3. Structural conformance.
	for _, f := range p.Fixed {
		if m.Get(f.I, f.J).Cmp(f.Value) != 0 {
			return fmt.Errorf("psdcomp: fixed entry (%d,%d) violated: got %s want %s",
				f.I, f.J, m.Get(f.I, f.J).RatString(), f.Value.RatString())
		}
	}
	for _, v := range p.Vars {
		got := m.Get(v.I, v.J)
		ok := false
		for _, c := range v.Candidates {
			if got.Cmp(c) == 0 {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf("psdcomp: variable (%d,%d) = %s is not in its candidate set",
				v.I, v.J, got.RatString())
		}
	}

	// 4. Trace consistency.
	if m.Trace().Cmp(r.Trace) != 0 {
		return fmt.Errorf("psdcomp: reported trace %s != matrix trace %s",
			r.Trace.RatString(), m.Trace().RatString())
	}

	// 5. Brute-force agreement.
	bf, err := BruteForce(p)
	if err != nil {
		return err
	}
	if !bf.Feasible {
		return fmt.Errorf("psdcomp: brute force found no solution but Solve did")
	}
	if bf.Trace.Cmp(r.Trace) != 0 {
		return fmt.Errorf("psdcomp: trace mismatch: solve=%s brute=%s",
			r.Trace.RatString(), bf.Trace.RatString())
	}
	if !matrixEqual(bf.Matrix, r.Matrix) {
		return fmt.Errorf("psdcomp: optimal matrix mismatch:\nsolve:\n%sbrute:\n%s",
			r.Matrix.String(), bf.Matrix.String())
	}
	return nil
}

func matrixEqual(a, b *Matrix) bool {
	if a.N != b.N {
		return false
	}
	for i := 0; i < a.N; i++ {
		for j := 0; j < a.N; j++ {
			if a.A[i][j].Cmp(b.A[i][j]) != 0 {
				return false
			}
		}
	}
	return true
}

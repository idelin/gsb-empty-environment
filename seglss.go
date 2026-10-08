// Package seglss computes optimal piecewise-constant approximations of an
// integer sequence. The objective is the sum of squared errors (each segment
// is fit by its mean) plus a fixed rational penalty per segment, and every
// segment must contain at least m items.
//
// Among all segmentations attaining the minimum penalized loss, the one with
// the fewest segments is chosen; remaining ties are broken by choosing the
// lexicographically smallest sequence of boundary (cut) positions.
//
// All arithmetic is exact (math/big), so losses and segment means are
// reported as exact rationals. A Solver precomputes prefix sums once, so a
// whole set of penalty parameters can be evaluated while reusing the same
// interval costs.
package seglss

import (
	"fmt"
	"math/big"
	"strings"
)

// Solver holds precomputed prefix sums of the sequence and of its squares,
// allowing interval costs to be reused across many Solve calls with
// different penalties.
type Solver struct {
	n   int
	m   int
	ps1 []big.Int // ps1[k] = sum of xs[:k]
	ps2 []big.Int // ps2[k] = sum of squares of xs[:k]
}

// NewSolver builds a Solver for the sequence xs with minimum segment length
// m (m must be >= 1).
func NewSolver(xs []int64, m int) (*Solver, error) {
	if m < 1 {
		return nil, fmt.Errorf("seglss: minimum segment length must be >= 1, got %d", m)
	}
	s := &Solver{n: len(xs), m: m, ps1: make([]big.Int, len(xs)+1), ps2: make([]big.Int, len(xs)+1)}
	for i, x := range xs {
		bx := big.NewInt(x)
		s.ps1[i+1].Add(&s.ps1[i], bx)
		s.ps2[i+1].Add(&s.ps2[i], new(big.Int).Mul(bx, bx))
	}
	return s, nil
}

// Cost returns the exact squared error of fitting xs[i:j] by its mean:
// sum(x^2) - sum(x)^2/len. It is independent of the penalty, so it is shared
// by every Solve call on this Solver.
func (s *Solver) Cost(i, j int) *big.Rat {
	var s1, s2 big.Int
	s1.Sub(&s.ps1[j], &s.ps1[i])
	s2.Sub(&s.ps2[j], &s.ps2[i])
	num := new(big.Int).Mul(&s1, &s1)
	num.Neg(num)
	r := new(big.Rat).SetInt(&s2)
	return r.Add(r, new(big.Rat).SetFrac(num, big.NewInt(int64(j-i))))
}

// mean returns the exact mean of xs[i:j].
func (s *Solver) mean(i, j int) *big.Rat {
	var s1 big.Int
	s1.Sub(&s.ps1[j], &s.ps1[i])
	return new(big.Rat).SetFrac(&s1, big.NewInt(int64(j-i)))
}

// Segment is one constant piece of the solution: the half-open index range
// [Start, End) and its exact mean.
type Segment struct {
	Start int
	End   int
	Mean  *big.Rat
}

// Result is the optimal segmentation for one penalty value.
type Result struct {
	Loss     *big.Rat  // total penalized loss: SSE + penalty*len(Segments)
	SSE      *big.Rat  // sum of squared errors without the penalty
	Segments []Segment // in order, covering [0, n)
}

// Cuts returns the internal boundary positions: the end index of every
// segment except the last.
func (r *Result) Cuts() []int {
	cuts := make([]int, 0, len(r.Segments)-1)
	for _, seg := range r.Segments[:len(r.Segments)-1] {
		cuts = append(cuts, seg.End)
	}
	return cuts
}

// String renders the exact loss and per-segment ranges and means.
func (r *Result) String() string {
	parts := make([]string, len(r.Segments))
	for i, seg := range r.Segments {
		parts[i] = fmt.Sprintf("[%d,%d) mean=%s", seg.Start, seg.End, seg.Mean.RatString())
	}
	return fmt.Sprintf("loss=%s sse=%s segments=%d {%s}",
		r.Loss.RatString(), r.SSE.RatString(), len(r.Segments), strings.Join(parts, " "))
}

// state is the optimal suffix value used by the dynamic program.
type state struct {
	loss *big.Rat // penalized loss of the suffix
	segs int      // number of segments used
	ok   bool     // whether the suffix can be segmented at all
}

// Solve finds the optimal segmentation for the given per-segment penalty.
// Ties in loss are broken by fewer segments, then by the lexicographically
// smallest boundary sequence.
func (s *Solver) Solve(penalty *big.Rat) (*Result, error) {
	n, m := s.n, s.m
	if n < m {
		return nil, fmt.Errorf("seglss: sequence of length %d cannot be segmented with minimum segment length %d", n, m)
	}

	// dp[i] = optimal penalized value for the suffix xs[i:]; dp[n] is the
	// empty suffix. A suffix of length < m (unless empty) is infeasible.
	dp := make([]state, n+1)
	dp[n] = state{loss: new(big.Rat), ok: true}
	for i := n - m; i >= 0; i-- {
		best := state{}
		for j := i + m; j <= n; j++ {
			if !dp[j].ok {
				continue
			}
			loss := s.Cost(i, j)
			loss.Add(loss, penalty)
			loss.Add(loss, dp[j].loss)
			cand := state{loss: loss, segs: dp[j].segs + 1, ok: true}
			if !best.ok || lessState(cand, best) {
				best = cand
			}
		}
		dp[i] = best
	}

	// Reconstruct the lexicographically smallest boundaries: at each
	// position take the smallest feasible cut that still attains dp[i].
	res := &Result{Loss: dp[0].loss, SSE: new(big.Rat)}
	for i := 0; i < n; {
		advanced := false
		for j := i + m; j <= n; j++ {
			if !dp[j].ok {
				continue
			}
			cost := s.Cost(i, j)
			total := new(big.Rat).Add(cost, penalty)
			total.Add(total, dp[j].loss)
			if total.Cmp(dp[i].loss) == 0 && dp[j].segs+1 == dp[i].segs {
				res.Segments = append(res.Segments, Segment{Start: i, End: j, Mean: s.mean(i, j)})
				res.SSE.Add(res.SSE, cost)
				i = j
				advanced = true
				break
			}
		}
		if !advanced {
			return nil, fmt.Errorf("seglss: internal error: no optimal cut from position %d", i)
		}
	}
	return res, nil
}

// lessState orders states lexicographically by (loss, segments).
func lessState(a, b state) bool {
	if c := a.loss.Cmp(b.loss); c != 0 {
		return c < 0
	}
	return a.segs < b.segs
}

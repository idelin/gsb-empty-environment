package psdcomp

import (
	"math/big"
	"math/rand"
	"testing"
)

func rats(t *testing.T, ss ...string) []*big.Rat {
	t.Helper()
	out := make([]*big.Rat, len(ss))
	for i, s := range ss {
		out[i] = rat(t, s)
	}
	return out
}

func mustSolve(t *testing.T, p *Problem) *Result {
	t.Helper()
	res, err := Solve(p)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if err := VerifyResult(p, res); err != nil {
		t.Fatalf("VerifyResult: %v", err)
	}
	return res
}

// 2x2 with unit diagonal and one free off-diagonal: PSD iff |x| <= 1.
// All feasible choices share trace 2, so the lexicographically smallest
// upper triangle wins: x = -1/2.
func TestSolveLexTieBreak(t *testing.T) {
	p := &Problem{
		N: 2,
		Fixed: []FixedEntry{
			{0, 0, big.NewRat(1, 1)},
			{1, 1, big.NewRat(1, 1)},
		},
		Vars: []Variable{
			{0, 1, rats(t, "-2", "-1/2", "0", "1/2", "2")},
		},
	}
	res := mustSolve(t, p)
	if !res.Feasible {
		t.Fatalf("expected feasible: %+v", res.Infeasible)
	}
	if res.Trace.Cmp(big.NewRat(2, 1)) != 0 {
		t.Fatalf("trace = %s, want 2", res.Trace.RatString())
	}
	if got := res.Matrix.Get(0, 1); got.Cmp(rat(t, "-1/2")) != 0 {
		t.Fatalf("x = %s, want -1/2", got.RatString())
	}
	if len(res.Certificate) != 3 {
		t.Fatalf("certificate has %d minors, want 3", len(res.Certificate))
	}
}

// Same setup but with {-1, 1} as the only feasible extremes: x = -1.
func TestSolveLexTieBreakExtremes(t *testing.T) {
	p := &Problem{
		N: 2,
		Fixed: []FixedEntry{
			{0, 0, big.NewRat(1, 1)},
			{1, 1, big.NewRat(1, 1)},
		},
		Vars: []Variable{
			{0, 1, rats(t, "-1", "-1/2", "1/2", "1")},
		},
	}
	res := mustSolve(t, p)
	if got := res.Matrix.Get(0, 1); got.Cmp(big.NewRat(-1, 1)) != 0 {
		t.Fatalf("x = %s, want -1", got.RatString())
	}
}

// [[a,1],[1,b]] with a,b in {0,1,2}: PSD iff a,b >= 0 and ab >= 1, so
// the minimum trace is 2 at a = b = 1.
func TestSolveTraceMinimization(t *testing.T) {
	p := &Problem{
		N:     2,
		Fixed: []FixedEntry{{0, 1, big.NewRat(1, 1)}},
		Vars: []Variable{
			{0, 0, rats(t, "0", "1", "2")},
			{1, 1, rats(t, "0", "1", "2")},
		},
	}
	res := mustSolve(t, p)
	if res.Trace.Cmp(big.NewRat(2, 1)) != 0 {
		t.Fatalf("trace = %s, want 2", res.Trace.RatString())
	}
	if res.Matrix.Get(0, 0).Cmp(big.NewRat(1, 1)) != 0 ||
		res.Matrix.Get(1, 1).Cmp(big.NewRat(1, 1)) != 0 {
		t.Fatalf("matrix =\n%s, want identity", res.Matrix.String())
	}
}

// Diagonal d of the 3x3 matrix with 2s off-diagonal. For every candidate
// d >= 0 the diagonal is nonnegative AND the determinant (d-2)^2(d+4) is
// nonnegative, so a "diagonal + determinant" check would wrongly accept
// d = 0 (trace 0). The correct answer, requiring ALL principal minors
// >= 0, is d = 2 with trace 6.
func TestSolveNeedsAllPrincipalMinors(t *testing.T) {
	p := &Problem{
		N: 3,
		Fixed: []FixedEntry{
			{0, 1, big.NewRat(2, 1)},
			{0, 2, big.NewRat(2, 1)},
			{1, 2, big.NewRat(2, 1)},
		},
		Vars: []Variable{
			{0, 0, rats(t, "0", "1", "2", "3")},
			{1, 1, rats(t, "0", "1", "2", "3")},
			{2, 2, rats(t, "0", "1", "2", "3")},
		},
	}
	res := mustSolve(t, p)
	if !res.Feasible {
		t.Fatalf("expected feasible")
	}
	if res.Trace.Cmp(big.NewRat(6, 1)) != 0 {
		t.Fatalf("trace = %s, want 6 (a diagonal+determinant check would give 0)", res.Trace.RatString())
	}
	for i := 0; i < 3; i++ {
		if res.Matrix.Get(i, i).Cmp(big.NewRat(2, 1)) != 0 {
			t.Fatalf("diagonal = %s, want all 2", res.Matrix.Get(i, i).RatString())
		}
	}
}

// Fixed entries already force a negative principal minor: infeasible,
// and the evidence must name the violating minor.
func TestSolveInfeasibleFixedMinor(t *testing.T) {
	p := &Problem{
		N: 2,
		Fixed: []FixedEntry{
			{0, 0, big.NewRat(1, 1)},
			{1, 1, big.NewRat(1, 1)},
			{0, 1, big.NewRat(2, 1)}, // det = 1 - 4 = -3
		},
	}
	res := mustSolve(t, p)
	if res.Feasible {
		t.Fatalf("expected infeasible")
	}
	if res.Infeasible == nil || len(res.Infeasible.Minors) == 0 {
		t.Fatalf("missing infeasibility evidence")
	}
	found := false
	for _, m := range res.Infeasible.Minors {
		if len(m.Indices) == 2 && m.Det.Cmp(big.NewRat(-3, 1)) == 0 {
			found = true
		}
	}
	if !found {
		t.Fatalf("evidence does not contain the -3 minor: %+v", res.Infeasible.Minors)
	}
}

// No candidate combination is PSD: infeasible without fixed-minor
// evidence.
func TestSolveInfeasibleNoCombination(t *testing.T) {
	p := &Problem{
		N: 2,
		Fixed: []FixedEntry{
			{0, 0, big.NewRat(1, 1)},
			{1, 1, big.NewRat(1, 1)},
		},
		Vars: []Variable{
			{0, 1, rats(t, "3/2", "2", "5/2")}, // all |x| > 1
		},
	}
	res := mustSolve(t, p)
	if res.Feasible {
		t.Fatalf("expected infeasible")
	}
}

// Pruning evidence: events must carry valid witnesses, and counts must
// match the aggregate statistics.
func TestPruneEvidence(t *testing.T) {
	p := &Problem{
		N: 3,
		Fixed: []FixedEntry{
			{0, 1, big.NewRat(2, 1)},
			{0, 2, big.NewRat(2, 1)},
			{1, 2, big.NewRat(2, 1)},
		},
		Vars: []Variable{
			{0, 0, rats(t, "0", "1", "2", "3")},
			{1, 1, rats(t, "0", "1", "2", "3")},
			{2, 2, rats(t, "0", "1", "2", "3")},
		},
	}
	var events []PruneEvent
	s := &Solver{OnPrune: func(ev PruneEvent) { events = append(events, ev) }}
	res, err := s.Solve(p)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if !res.Feasible {
		t.Fatalf("expected feasible")
	}
	var nMinor, nTrace uint64
	for _, ev := range events {
		if len(ev.Prefix) != ev.Depth {
			t.Fatalf("event prefix length %d != depth %d", len(ev.Prefix), ev.Depth)
		}
		switch ev.Reason {
		case PruneMinorViolation:
			nMinor++
			if ev.Minor == nil || ev.Minor.Det.Sign() >= 0 {
				t.Fatalf("minor event without negative witness: %+v", ev)
			}
		case PruneTraceBound:
			nTrace++
			if ev.Bound == nil || ev.Best == nil || ev.Bound.Cmp(ev.Best) <= 0 {
				t.Fatalf("trace event without bound > best: %+v", ev)
			}
		default:
			t.Fatalf("unknown reason %v", ev.Reason)
		}
	}
	if nMinor != res.Stats.PrunedMinor || nTrace != res.Stats.PrunedTrace {
		t.Fatalf("event counts (%d,%d) != stats (%d,%d)",
			nMinor, nTrace, res.Stats.PrunedMinor, res.Stats.PrunedTrace)
	}
	if res.Stats.PrunedMinor == 0 {
		t.Fatalf("expected at least one minor pruning on this instance")
	}
	// Every pruned branch plus every leaf accounts for every tried node
	// exactly once: nodes = leaves + pruned + internal expansions.
	if res.Stats.Nodes < res.Stats.Leaves+res.Stats.PrunedMinor+res.Stats.PrunedTrace {
		t.Fatalf("inconsistent stats: %+v", res.Stats)
	}
}

// Randomized cross-validation: Solve (minor-based, pruned) must agree
// with the independent BruteForce (elimination-based, unpruned) via
// VerifyResult, on random instances.
func TestSolveMatchesBruteForceRandom(t *testing.T) {
	r := rand.New(rand.NewSource(99))
	for trial := 0; trial < 200; trial++ {
		n := 1 + r.Intn(MaxN)
		p := &Problem{N: n}
		for i := 0; i < n; i++ {
			for j := i; j < n; j++ {
				if r.Intn(2) == 0 {
					p.Fixed = append(p.Fixed, FixedEntry{i, j, randomRat(r, 3)})
					continue
				}
				k := 1 + r.Intn(3)
				var cs []*big.Rat
				for c := 0; c < k; c++ {
					cs = append(cs, randomRat(r, 3))
				}
				p.Vars = append(p.Vars, Variable{i, j, cs})
			}
		}
		res, err := Solve(p)
		if err != nil {
			t.Fatalf("trial %d: Solve: %v", trial, err)
		}
		if err := VerifyResult(p, res); err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
	}
}

// The combination limit from the problem statement must be enforced.
func TestComboLimitEnforced(t *testing.T) {
	p := &Problem{N: 4}
	cs := rats(t, "0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "10") // 11
	for i := 0; i < 4; i++ {
		for j := i; j < 4; j++ {
			p.Vars = append(p.Vars, Variable{i, j, cs})
		}
	}
	// 11^10 combinations >> 1e6.
	if err := p.Validate(); err == nil {
		t.Fatalf("expected combination-limit error")
	}
	if _, err := Solve(p); err == nil {
		t.Fatalf("expected Solve to reject oversized problem")
	}
}

// A problem with no variables at all: the fixed matrix itself is the
// only candidate.
func TestSolveNoVariables(t *testing.T) {
	p := &Problem{
		N: 2,
		Fixed: []FixedEntry{
			{0, 0, big.NewRat(1, 1)},
			{1, 1, big.NewRat(0, 1)},
			{0, 1, big.NewRat(0, 1)},
		},
	}
	res := mustSolve(t, p)
	if !res.Feasible || res.Trace.Cmp(big.NewRat(1, 1)) != 0 {
		t.Fatalf("unexpected result: %+v", res)
	}
	if len(res.Certificate) != 3 {
		t.Fatalf("certificate has %d minors, want 3", len(res.Certificate))
	}
}

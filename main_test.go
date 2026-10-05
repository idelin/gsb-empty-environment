package main

import (
	"math/rand"
	"testing"
)

func cptr(v int64) *int64 { return &v }

func mkInput(ivs [][2]int64, xs []int64, costs []int64) rawInput {
	raw := rawInput{Intervals: ivs}
	for i, x := range xs {
		raw.Candidates = append(raw.Candidates, rawCandidate{X: x, Cost: cptr(costs[i])})
	}
	f := false
	raw.Verify = &f
	return raw
}

func TestSharedCoverageBeatsIndependentCheapest(t *testing.T) {
	// Cheapest point of [1,3] is x=1 (cost 4), of [3,5] is x=5 (cost 4);
	// picking them independently costs 8, but the shared point x=3 costs 5.
	raw := mkInput([][2]int64{{1, 3}, {3, 5}}, []int64{1, 3, 5}, []int64{4, 5, 4})
	out := solve(raw)
	if out.Status != "optimal" {
		t.Fatalf("status = %s, want optimal", out.Status)
	}
	if *out.Cost != 5 || *out.Count != 1 || len(out.Points) != 1 || out.Points[0] != 3 {
		t.Fatalf("got cost=%d count=%d points=%v, want cost=5 count=1 points=[3]",
			*out.Cost, *out.Count, out.Points)
	}
}

func TestCountTieBreak(t *testing.T) {
	// All costs zero: minimize the number of points, then lexicographic.
	raw := mkInput([][2]int64{{1, 2}, {4, 5}}, []int64{1, 2, 4, 5}, []int64{0, 0, 0, 0})
	out := solve(raw)
	if *out.Cost != 0 || *out.Count != 2 {
		t.Fatalf("got cost=%d count=%d, want 0/2", *out.Cost, *out.Count)
	}
	if out.Points[0] != 1 || out.Points[1] != 4 {
		t.Fatalf("got points=%v, want [1 4]", out.Points)
	}
}

func TestLexTieBreak(t *testing.T) {
	raw := mkInput([][2]int64{{1, 10}}, []int64{6, 5}, []int64{0, 0})
	out := solve(raw)
	if *out.Count != 1 || out.Points[0] != 5 {
		t.Fatalf("got points=%v, want [5]", out.Points)
	}
}

func TestInfeasibleEvidence(t *testing.T) {
	raw := mkInput([][2]int64{{1, 3}, {10, 20}}, []int64{2, 25}, []int64{1, 1})
	out := solve(raw)
	if out.Status != "infeasible" {
		t.Fatalf("status = %s, want infeasible", out.Status)
	}
	if len(out.Infeasible) != 1 || out.Infeasible[0].Index != 1 {
		t.Fatalf("evidence = %+v, want single entry for interval 1", out.Infeasible)
	}
	e := out.Infeasible[0]
	if e.NearestCandidateBelow == nil || *e.NearestCandidateBelow != 2 {
		t.Fatalf("nearest below = %v, want 2", e.NearestCandidateBelow)
	}
	if e.NearestCandidateAbove == nil || *e.NearestCandidateAbove != 25 {
		t.Fatalf("nearest above = %v, want 25", e.NearestCandidateAbove)
	}
}

func TestDuplicateCandidatesKeepCheapest(t *testing.T) {
	raw := rawInput{Intervals: [][2]int64{{1, 3}}}
	raw.Candidates = []rawCandidate{{X: 2, Cost: cptr(5)}, {X: 2, Cost: cptr(3)}}
	out := solve(raw)
	if out.Status != "optimal" || *out.Cost != 3 {
		t.Fatalf("got status=%s cost=%v, want optimal cost 3", out.Status, out.Cost)
	}
	if out.Stats.DuplicatesRemoved != 1 {
		t.Fatalf("duplicatesRemoved = %d, want 1", out.Stats.DuplicatesRemoved)
	}
}

func TestNegativeCostRejected(t *testing.T) {
	raw := mkInput([][2]int64{{1, 3}}, []int64{2}, []int64{-1})
	out := solve(raw)
	if out.Status != "error" {
		t.Fatalf("status = %s, want error", out.Status)
	}
}

func TestNoIntervals(t *testing.T) {
	raw := mkInput(nil, []int64{2}, []int64{7})
	out := solve(raw)
	if out.Status != "optimal" || *out.Cost != 0 || *out.Count != 0 {
		t.Fatalf("got status=%s cost=%v count=%v", out.Status, out.Cost, out.Count)
	}
}

func TestInterruptedBounds(t *testing.T) {
	raw := mkInput([][2]int64{{1, 2}, {3, 4}}, []int64{1, 2, 3, 4}, []int64{1, 1, 1, 1})
	raw.MaxNodes = 1
	out := solve(raw)
	if out.Status != "interrupted" {
		t.Fatalf("status = %s, want interrupted", out.Status)
	}
	if out.Bounds == nil || out.Bounds.UpperCost == nil {
		t.Fatalf("missing bounds: %+v", out.Bounds)
	}
	if out.Bounds.LowerCost > *out.Bounds.UpperCost {
		t.Fatalf("lower %d > upper %d", out.Bounds.LowerCost, *out.Bounds.UpperCost)
	}
	if *out.Bounds.UpperCost != *out.Cost {
		t.Fatalf("upper %d != incumbent cost %d", *out.Bounds.UpperCost, *out.Cost)
	}
	// The true optimum is 2; the bounds must enclose it.
	if out.Bounds.LowerCost > 2 || *out.Bounds.UpperCost < 2 {
		t.Fatalf("bounds [%d, %d] do not enclose optimum 2", out.Bounds.LowerCost, *out.Bounds.UpperCost)
	}
}

func TestProofValid(t *testing.T) {
	raw := mkInput([][2]int64{{1, 4}, {2, 6}, {5, 9}, {8, 10}},
		[]int64{1, 3, 5, 6, 8, 10}, []int64{2, 4, 1, 3, 2, 5})
	out := solve(raw)
	if out.Status != "optimal" {
		t.Fatalf("status = %s", out.Status)
	}
	inSet := make(map[int64]bool)
	for _, p := range out.Points {
		inSet[p] = true
	}
	if len(out.Proof) != 4 {
		t.Fatalf("proof length = %d, want 4", len(out.Proof))
	}
	for _, e := range out.Proof {
		if e.Point < e.Interval[0] || e.Point > e.Interval[1] {
			t.Fatalf("proof point %d outside %v", e.Point, e.Interval)
		}
		if !inSet[e.Point] {
			t.Fatalf("proof point %d not in solution", e.Point)
		}
	}
}

func TestRandomCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	tr := true
	for iter := 0; iter < 400; iter++ {
		n := 1 + rng.Intn(9)
		m := 1 + rng.Intn(7)
		var raw rawInput
		raw.Verify = &tr
		raw.VerifyMax = 20
		for j := 0; j < n; j++ {
			raw.Candidates = append(raw.Candidates, rawCandidate{
				X:    int64(rng.Intn(13)),
				Cost: cptr(int64(rng.Intn(7))),
			})
		}
		for i := 0; i < m; i++ {
			l := int64(rng.Intn(13))
			r := l + int64(rng.Intn(4))
			raw.Intervals = append(raw.Intervals, [2]int64{l, r})
		}
		out := solve(raw)
		switch out.Status {
		case "infeasible":
			for _, e := range out.Infeasible {
				for _, c := range raw.Candidates {
					if c.X >= e.Interval[0] && c.X <= e.Interval[1] {
						t.Fatalf("iter %d: interval %v flagged infeasible but contains %d",
							iter, e.Interval, c.X)
					}
				}
			}
		case "optimal":
			if out.Verification == nil || !out.Verification.Performed {
				t.Fatalf("iter %d: verification not performed", iter)
			}
			if out.Verification.MatchesSolver == nil || !*out.Verification.MatchesSolver {
				t.Fatalf("iter %d: solver/enum mismatch: solver cost=%d pts=%v, enum=%+v",
					iter, *out.Cost, out.Points, out.Verification)
			}
			if out.Bounds.LowerCost != *out.Cost || *out.Bounds.UpperCost != *out.Cost {
				t.Fatalf("iter %d: optimal but bounds [%d, %d] != cost %d",
					iter, out.Bounds.LowerCost, *out.Bounds.UpperCost, *out.Cost)
			}
			inSet := make(map[int64]bool)
			for _, p := range out.Points {
				inSet[p] = true
			}
			for _, e := range out.Proof {
				if e.Point < e.Interval[0] || e.Point > e.Interval[1] || !inSet[e.Point] {
					t.Fatalf("iter %d: bad proof entry %+v", iter, e)
				}
			}
		default:
			t.Fatalf("iter %d: unexpected status %s (%s)", iter, out.Status, out.Error)
		}
	}
}

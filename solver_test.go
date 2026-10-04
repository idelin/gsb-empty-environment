package main

import (
	"math/rand"
	"testing"
)

func buildProblem(t *testing.T, in *Input) *problem {
	t.Helper()
	p, err := prepare(in)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	return p
}

func solveAll(p *problem) *Solver {
	s := &Solver{p: p}
	if len(p.infeasible) == 0 {
		g, c, ok := greedy(p)
		if !ok {
			panic("greedy infeasible despite no infeasible intervals")
		}
		s.best, s.bestCost, s.hasBest = sortedCopy(g), c, true
		s.search(make([]uint64, p.nWords), nil, 0)
	}
	return s
}

func checkFeasible(t *testing.T, in *Input, pts []int64) {
	t.Helper()
	for i, iv := range in.Intervals {
		ok := false
		for _, pt := range pts {
			if iv.Lo <= pt && pt <= iv.Hi {
				ok = true
				break
			}
		}
		if !ok {
			t.Fatalf("interval %d %v not stabbed by %v", i, iv, pts)
		}
	}
}

// A single shared point must beat independently chosen per-interval cheapest
// points: cheapest for [1,3] is 1 (6), cheapest for [2,4] is 4 (6), but the
// shared point 2 (10) stabs both for less.
func TestSharedCoverageBeatsIndependent(t *testing.T) {
	in := &Input{
		Intervals: []Interval{{1, 3}, {2, 4}},
		Candidates: []Candidate{
			{Point: 2, Cost: 10},
			{Point: 1, Cost: 6},
			{Point: 4, Cost: 6},
		},
	}
	p := buildProblem(t, in)
	s := solveAll(p)
	if s.interrupted || !s.hasBest {
		t.Fatal("no solution")
	}
	if s.bestCost != 10 || !equalPoints(s.best, []int64{2}) {
		t.Fatalf("got cost=%d points=%v, want cost=10 points=[2]", s.bestCost, s.best)
	}
	checkFeasible(t, in, s.best)
}

// Equal cost: fewer points win.
func TestTieBreakFewerPoints(t *testing.T) {
	in := &Input{
		Intervals: []Interval{{1, 5}, {3, 7}},
		Candidates: []Candidate{
			{Point: 4, Cost: 12}, // stabs both intervals alone
			{Point: 2, Cost: 6},  // stabs only [1,5]
			{Point: 6, Cost: 6},  // stabs only [3,7]
		},
	}
	s := solveAll(buildProblem(t, in))
	if s.bestCost != 12 || !equalPoints(s.best, []int64{4}) {
		t.Fatalf("got cost=%d points=%v, want cost=12 points=[4]", s.bestCost, s.best)
	}
}

// Equal cost and count: lexicographically smallest point list wins.
func TestTieBreakLexicographic(t *testing.T) {
	in := &Input{
		Intervals: []Interval{{1, 10}},
		Candidates: []Candidate{
			{Point: 7, Cost: 5},
			{Point: 3, Cost: 5},
		},
	}
	s := solveAll(buildProblem(t, in))
	if s.bestCost != 5 || !equalPoints(s.best, []int64{3}) {
		t.Fatalf("got cost=%d points=%v, want cost=5 points=[3]", s.bestCost, s.best)
	}
}

// Zero-cost points are allowed; free shared coverage must be used.
func TestZeroCostShared(t *testing.T) {
	in := &Input{
		Intervals: []Interval{{1, 5}, {3, 8}},
		Candidates: []Candidate{
			{Point: 4, Cost: 0},
			{Point: 1, Cost: 1},
		},
	}
	s := solveAll(buildProblem(t, in))
	if s.bestCost != 0 || !equalPoints(s.best, []int64{4}) {
		t.Fatalf("got cost=%d points=%v, want cost=0 points=[4]", s.bestCost, s.best)
	}
}

// Intervals with no candidate inside must be reported as evidence.
func TestInfeasibleEvidence(t *testing.T) {
	in := &Input{
		Intervals:  []Interval{{1, 2}, {10, 12}, {5, 6}},
		Candidates: []Candidate{{Point: 1, Cost: 3}, {Point: 5, Cost: 2}},
	}
	p := buildProblem(t, in)
	if len(p.infeasible) != 1 || p.infeasible[0] != 1 {
		t.Fatalf("infeasible evidence = %v, want [1]", p.infeasible)
	}
	er := enumerate(p, 22)
	if er.Feasible {
		t.Fatal("enumeration unexpectedly feasible")
	}
}

// Duplicate candidate points keep the cheapest cost; useless candidates are
// dropped without affecting the optimum.
func TestNormalization(t *testing.T) {
	in := &Input{
		Intervals: []Interval{{1, 5}},
		Candidates: []Candidate{
			{Point: 3, Cost: 9},
			{Point: 3, Cost: 4},
			{Point: 100, Cost: 1}, // stabs nothing
		},
	}
	p := buildProblem(t, in)
	if p.dropped != 1 || len(p.points) != 1 || p.costs[0] != 4 {
		t.Fatalf("points=%v costs=%v dropped=%d", p.points, p.costs, p.dropped)
	}
	s := solveAll(p)
	if s.bestCost != 4 || !equalPoints(s.best, []int64{3}) {
		t.Fatalf("got cost=%d points=%v, want cost=4 points=[3]", s.bestCost, s.best)
	}
}

// Randomized cross-check: branch-and-bound must agree with exhaustive
// subset enumeration on the full (cost, count, lexicographic) order.
func TestRandomizedCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for iter := 0; iter < 400; iter++ {
		n := 1 + rng.Intn(7)
		in := &Input{}
		for i := 0; i < n; i++ {
			lo := int64(rng.Intn(11))
			hi := lo + int64(rng.Intn(6))
			in.Intervals = append(in.Intervals, Interval{lo, hi})
		}
		m := rng.Intn(11)
		for j := 0; j < m; j++ {
			in.Candidates = append(in.Candidates, Candidate{
				Point: int64(rng.Intn(16)),
				Cost:  int64(rng.Intn(10)),
			})
		}
		p, err := prepare(in)
		if err != nil {
			t.Fatalf("iter %d: %v", iter, err)
		}
		er := enumerate(p, 22)
		if len(p.infeasible) > 0 {
			if er.Feasible {
				t.Fatalf("iter %d: solver infeasible %v but enumeration feasible", iter, p.infeasible)
			}
			continue
		}
		if !er.Feasible {
			t.Fatalf("iter %d: enumeration infeasible but no evidence", iter)
		}
		s := solveAll(p)
		if s.interrupted {
			t.Fatalf("iter %d: unexpected interruption", iter)
		}
		if s.bestCost != er.Cost || !equalPoints(s.best, er.Points) {
			t.Fatalf("iter %d: solver cost=%d pts=%v, enum cost=%d pts=%v",
				iter, s.bestCost, s.best, er.Cost, er.Points)
		}
		checkFeasible(t, in, s.best)
	}
}

// On interruption the reported bounds must bracket the true optimum.
func TestInterruptedBounds(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	in := &Input{}
	for i := 0; i < 12; i++ {
		lo := int64(rng.Intn(20))
		in.Intervals = append(in.Intervals, Interval{lo, lo + int64(1+rng.Intn(6))})
	}
	for j := 0; j < 18; j++ {
		in.Candidates = append(in.Candidates, Candidate{
			Point: int64(rng.Intn(28)),
			Cost:  int64(rng.Intn(10)),
		})
	}
	p, err := prepare(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.infeasible) > 0 {
		t.Skip("random instance infeasible")
	}
	er := enumerate(p, 22)
	if !er.Feasible {
		t.Skip("random instance infeasible")
	}
	s := &Solver{p: p, nodeLimit: 3}
	g, c, _ := greedy(p)
	s.best, s.bestCost, s.hasBest = sortedCopy(g), c, true
	s.search(make([]uint64, p.nWords), nil, 0)
	if !s.interrupted {
		t.Skip("search finished within node limit")
	}
	lb, ok := s.lowerBound()
	if !ok {
		t.Fatal("no lower bound available")
	}
	if lb > er.Cost || er.Cost > s.bestCost {
		t.Fatalf("bounds [%d, %d] do not bracket optimum %d", lb, s.bestCost, er.Cost)
	}
	checkFeasible(t, in, s.best)
}

package sdf

import (
	"testing"
)

func pipelineProblem() *Problem {
	return &Problem{
		Actors: 3,
		Edges: []Edge{
			{Src: -1, Dst: 0, Cons: 1},         // 0: input
			{Src: 0, Dst: 1, Prod: 1, Cons: 1}, // 1
			{Src: 1, Dst: 2, Prod: 1, Cons: 1}, // 2
			{Src: 2, Dst: -1, Prod: 1},         // 3: output
		},
		Cost:    []int64{1, 1, 1},
		Res:     Resources{Descriptors: 3},
		WindowN: 4,
		WindowM: 1,
		MaxCap:  4,
	}
}

func feed(p *Problem, period, count, gap, runs int64) *Problem {
	p.Inputs = []Input{{EdgeID: 0, Period: period, Count: count, FirstTime: 0, Gap: gap, Runs: runs}}
	return p
}

func TestFeasiblePipelineMinimumCap(t *testing.T) {
	p := feed(pipelineProblem(), 4, 1, 4, 1)
	r, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != StatusFeasible {
		t.Fatalf("status=%s reason=%s", r.Status, r.Reason)
	}
	// Input edge needs two slots (one queued behind a live consumer); every
	// internal single-rate edge and the output edge need one.
	want := []int{2, 1, 1, 1}
	if len(r.Cap) != len(want) {
		t.Fatalf("cap=%v", r.Cap)
	}
	for i := range want {
		if r.Cap[i] != want[i] {
			t.Fatalf("cap=%v want %v", r.Cap, want)
		}
	}
	if r.TotalCap != 5 {
		t.Fatalf("total=%d", r.TotalCap)
	}
	if r.Schedule == nil {
		t.Fatal("missing schedule")
	}
	if r.Schedule.CycleTime <= 0 || r.Schedule.CycleInputs <= 0 || r.Schedule.CycleOutputs <= 0 {
		t.Fatalf("bad cycle: %+v", r.Schedule)
	}
	if err := r.Schedule.Verify(p, 5); err != nil {
		t.Fatalf("replay over 5 cycles: %v", err)
	}
	if err := VerifyCertificate(p, r.Cap, r.Cert); err != nil {
		t.Fatalf("certificate: %v", err)
	}
}

func TestInconsistentRates(t *testing.T) {
	// Two actors joined by two parallel edges with incompatible rate ratios:
	// one demands qA/qB = 1/2, the other qA/qB = 2/1. No positive solution.
	p := &Problem{
		Actors: 2,
		Edges: []Edge{
			{Src: -1, Dst: 0, Cons: 1},
			{Src: 0, Dst: 1, Prod: 1, Cons: 2},
			{Src: 0, Dst: 1, Prod: 2, Cons: 1},
			{Src: 1, Dst: -1, Prod: 1},
		},
		Cost:    []int64{1, 1},
		Res:     Resources{Descriptors: 2},
		WindowN: 4,
		WindowM: 1,
		MaxCap:  4,
	}
	feed(p, 4, 1, 4, 1)
	r, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != StatusInconsistent {
		t.Fatalf("status=%s", r.Status)
	}
}

func TestFeedbackInsufficientDelay(t *testing.T) {
	// Actor 0 self-loop consumes one token but starts with no delay token.
	p := feed(pipelineProblem(), 4, 1, 4, 1)
	p.Edges = append(p.Edges, Edge{Src: 0, Dst: 0, Prod: 1, Cons: 1, Delay: 0})
	r, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != StatusDeadlock {
		t.Fatalf("status=%s reason=%s", r.Status, r.Reason)
	}

	// One preloaded delay token makes the same feedback edge viable.
	p2 := feed(pipelineProblem(), 4, 1, 4, 1)
	p2.Edges = append(p2.Edges, Edge{Src: 0, Dst: 0, Prod: 1, Cons: 1, Delay: 1})
	r2, err := Solve(p2)
	if err != nil {
		t.Fatal(err)
	}
	if r2.Status != StatusFeasible {
		t.Fatalf("status=%s reason=%s", r2.Status, r2.Reason)
	}
	if err := r2.Schedule.Verify(p2, 3); err != nil {
		t.Fatalf("replay: %v", err)
	}
}

func TestOverrunByInFlightReservation(t *testing.T) {
	// Offer tokens faster than the three-stage pipeline can drain them: the
	// input edge backlog grows without bound even though a firing is always
	// running, because output slots of in-flight firings stay reserved.
	p := feed(pipelineProblem(), 4, 2, 1, 2)
	r, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != StatusOverrun {
		t.Fatalf("status=%s reason=%s", r.Status, r.Reason)
	}
}

func TestAverageOKButSlidingWindowFails(t *testing.T) {
	// Bursty input: two tokens back-to-back then a long idle gap. Long-run
	// average throughput is sufficient, but the first tight window cannot get
	// two outputs committed in time through the serial pipeline.
	p := feed(pipelineProblem(), 10, 2, 1, 2)
	p.WindowN = 2
	p.WindowM = 2
	r, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != StatusWindow {
		t.Fatalf("status=%s reason=%s", r.Status, r.Reason)
	}

	// The same graph with a relaxed window is feasible.
	p2 := feed(pipelineProblem(), 10, 2, 1, 2)
	p2.WindowN = 10
	p2.WindowM = 2
	r2, err := Solve(p2)
	if err != nil {
		t.Fatal(err)
	}
	if r2.Status != StatusFeasible {
		t.Fatalf("status=%s reason=%s", r2.Status, r2.Reason)
	}
	if err := r2.Schedule.Verify(p2, 4); err != nil {
		t.Fatalf("replay: %v", err)
	}
}

func TestLongRunNoIdleTickIteration(t *testing.T) {
	// One token per huge period: idle gaps around 2^40 are represented
	// arithmetically. The solver must finish quickly without walking ticks.
	p := feed(pipelineProblem(), 1<<40, 1, 1<<40, 1)
	r, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != StatusFeasible {
		t.Fatalf("status=%s reason=%s", r.Status, r.Reason)
	}
	if r.Schedule.CycleTime < (1 << 40) {
		t.Fatalf("cycle time %d does not span the huge input period", r.Schedule.CycleTime)
	}
	if err := r.Schedule.Verify(p, 2); err != nil {
		t.Fatalf("replay: %v", err)
	}
}

func TestLexicographicTieBreak(t *testing.T) {
	// A homogeneous ring admits several equal-total capacity vectors; the
	// solver must return the lexicographically smallest by edge id.
	p := &Problem{
		Actors: 2,
		Edges: []Edge{
			{Src: -1, Dst: 0, Cons: 1},
			{Src: 0, Dst: 1, Prod: 1, Cons: 1, Delay: 1},
			{Src: 1, Dst: 0, Prod: 1, Cons: 1},
			{Src: 1, Dst: -1, Prod: 1},
		},
		Cost:    []int64{1, 1},
		Res:     Resources{Descriptors: 2},
		WindowN: 4,
		WindowM: 1,
		MaxCap:  4,
	}
	feed(p, 4, 1, 4, 1)
	r, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != StatusFeasible {
		t.Fatalf("status=%s reason=%s", r.Status, r.Reason)
	}
	// Enumerate every equally-small vector and confirm none beats r.Cap.
	idx, _ := p.validate()
	q, _, _ := repVector(p, idx)
	bestTotal := r.TotalCap
	enumerateVectors(r.Cert.EdgeLowerBound, make([]bool, len(p.Edges)), p.MaxCap, func(caps []int) bool {
		total := 0
		for _, c := range caps {
			total += c
		}
		if total < bestTotal {
			t.Fatalf("vector %v smaller than reported", caps)
		}
		if total == bestTotal {
			for e := range caps {
				if caps[e] < r.Cap[e] {
					t.Fatalf("lexicographic vector %v beats %v", caps, r.Cap)
				}
				if caps[e] > r.Cap[e] {
					break
				}
			}
			if evaluate(p, idx, caps, q).status == StatusFeasible && lexicographic(caps, r.Cap) {
				t.Fatalf("feasible vector %v should have been chosen over %v", caps, r.Cap)
			}
		}
		return total <= bestTotal
	})
}

func lexicographic(a, b []int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

func TestRepetitionVectorRates(t *testing.T) {
	// Classic SDF: A produces 2, B consumes 3 on one edge; B produces 1,
	// C consumes 2 on another. Primitive balance is q = (3, 2, 1).
	p := &Problem{
		Actors: 3,
		Edges: []Edge{
			{Src: -1, Dst: 0, Cons: 2},
			{Src: 0, Dst: 1, Prod: 2, Cons: 3},
			{Src: 1, Dst: 2, Prod: 1, Cons: 2},
			{Src: 2, Dst: -1, Prod: 1},
		},
		Cost:    []int64{1, 1, 1},
		Res:     Resources{Descriptors: 2},
		WindowN: 6,
		WindowM: 1,
		MaxCap:  4,
	}
	p.Inputs = []Input{{EdgeID: 0, Period: 12, Count: 6, FirstTime: 0, Gap: 2, Runs: 6}}
	idx, err := p.validate()
	if err != nil {
		t.Fatal(err)
	}
	q, _, ok := repVector(p, idx)
	if !ok {
		t.Fatal("expected consistent rates")
	}
	want := []int64{3, 2, 1}
	for i := range want {
		if q[i] != want[i] {
			t.Fatalf("q=%v want %v", q, want)
		}
	}
}

func TestMalformedRejected(t *testing.T) {
	p := feed(pipelineProblem(), 4, 1, 4, 1)
	p.Res.Descriptors = 0
	if _, err := Solve(p); err == nil {
		t.Fatal("expected descriptor validation error")
	}
	p2 := feed(pipelineProblem(), 4, 1, 4, 1)
	p2.WindowM = p2.WindowN + 1
	if _, err := Solve(p2); err == nil {
		t.Fatal("expected window validation error")
	}
}

package sdf

import "testing"

func TestDescriptorLimitBoundsInflight(t *testing.T) {
	// Five actors in a chain with a one-tick firing cost. With one descriptor
	// only a single firing can be in flight at a time; the replay of any
	// produced schedule must never exceed that pool.
	p := &Problem{
		Actors: 5,
		Edges: []Edge{
			{Src: -1, Dst: 0, Cons: 1},
			{Src: 0, Dst: 1, Prod: 1, Cons: 1},
			{Src: 1, Dst: 2, Prod: 1, Cons: 1},
			{Src: 2, Dst: 3, Prod: 1, Cons: 1},
			{Src: 3, Dst: 4, Prod: 1, Cons: 1},
			{Src: 4, Dst: -1, Prod: 1},
		},
		Cost:    []int64{1, 1, 1, 1, 1},
		Res:     Resources{Descriptors: 1},
		WindowN: 8,
		WindowM: 1,
		MaxCap:  4,
	}
	p.Inputs = []Input{{EdgeID: 0, Period: 8, Count: 1, FirstTime: 0, Gap: 8, Runs: 1}}
	r, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != StatusFeasible {
		t.Fatalf("status=%s reason=%s", r.Status, r.Reason)
	}
	if err := r.Schedule.Verify(p, 3); err != nil {
		t.Fatalf("single-descriptor replay: %v", err)
	}

	// More descriptors cannot make a feasible instance infeasible and must
	// still replay cleanly.
	p.Res.Descriptors = 4
	r2, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if r2.Status != StatusFeasible {
		t.Fatalf("status=%s reason=%s", r2.Status, r2.Reason)
	}
	if err := r2.Schedule.Verify(p, 3); err != nil {
		t.Fatalf("four-descriptor replay: %v", err)
	}
}

func TestSixActorFeedbackGraph(t *testing.T) {
	// Six actors, including a feedback edge preloaded with delay tokens, and
	// non-unit production/consumption rates that still admit a repetition
	// vector. Exercises the full path on the largest supported graph.
	p := &Problem{
		Actors: 6,
		// Repetition vector q = (1,2,1,1,1,1): actor 1 fires twice per cycle.
		Edges: []Edge{
			{Src: -1, Dst: 0, Cons: 1},                   // 0 input
			{Src: 0, Dst: 1, Prod: 2, Cons: 1},           // 1: 2*q0 = 1*q1
			{Src: 1, Dst: 2, Prod: 1, Cons: 2},           // 2: 1*q1 = 2*q2
			{Src: 2, Dst: 3, Prod: 1, Cons: 1},           // 3
			{Src: 3, Dst: 4, Prod: 1, Cons: 1},           // 4
			{Src: 4, Dst: 5, Prod: 1, Cons: 1},           // 5
			{Src: 5, Dst: 2, Prod: 1, Cons: 1, Delay: 2}, // 6 feedback
			{Src: 5, Dst: -1, Prod: 1},                   // 7 output
		},
		Cost:    []int64{2, 3, 1, 4, 2, 5},
		Res:     Resources{Descriptors: 3},
		WindowN: 16,
		WindowM: 2,
		MaxCap:  4,
	}
	p.Inputs = []Input{{EdgeID: 0, Period: 32, Count: 1, FirstTime: 0, Gap: 32, Runs: 1}}
	r, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status == StatusInconclusive {
		t.Skipf("exact envelope exhausted: %s", r.Reason)
	}
	if r.Status != StatusFeasible {
		t.Fatalf("status=%s reason=%s", r.Status, r.Reason)
	}
	for eid, c := range r.Cap {
		if c < 0 || c > p.MaxCap {
			t.Fatalf("edge %d cap %d out of range", eid, c)
		}
	}
	if err := r.Schedule.Verify(p, 3); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if err := VerifyCertificate(p, r.Cap, r.Cert); err != nil {
		t.Fatalf("certificate: %v", err)
	}
	// Every reported firing count over one cycle must respect rate balance.
	q := r.Repetition
	for eid, e := range p.Edges {
		if e.IsInput() || e.IsOutput() {
			continue
		}
		if int64(e.Prod)*q[e.Src] != int64(e.Cons)*q[e.Dst] {
			t.Fatalf("edge %d balance broken", eid)
		}
	}
}

func TestHugeCostsAndIdleDoNotIterateTicks(t *testing.T) {
	// Firing cost and input idle gap both near 2^45. The analysis must remain
	// arithmetic and finish promptly.
	const huge = int64(1) << 45
	p := pipelineProblem()
	p.Cost = []int64{huge / 4, huge / 4, huge / 2}
	p.Inputs = []Input{{EdgeID: 0, Period: huge, Count: 1, FirstTime: 0, Gap: huge, Runs: 1}}
	p.WindowN = 2
	p.WindowM = 1
	r, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != StatusFeasible {
		t.Fatalf("status=%s reason=%s", r.Status, r.Reason)
	}
	if r.Schedule.CycleTime < huge {
		t.Fatalf("cycle %d shorter than input period %d", r.Schedule.CycleTime, huge)
	}
	if err := r.Schedule.Verify(p, 2); err != nil {
		t.Fatalf("replay: %v", err)
	}
}

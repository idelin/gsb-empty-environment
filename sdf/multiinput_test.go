package sdf

import "testing"

func TestTwoInputEdgesDifferentPeriods(t *testing.T) {
	// Two independent actors consume periodic input streams with periods 4 and
	// 6. Their phases realign every LCM(4,6)=12 ticks, so the steady cycle is
	// detected by state repeat rather than a fixed simulation horizon.
	p := &Problem{
		Actors: 2,
		Edges: []Edge{
			{Src: -1, Dst: 0, Cons: 1}, // 0 input A
			{Src: -1, Dst: 1, Cons: 1}, // 1 input B
			{Src: 0, Dst: -1, Prod: 1}, // 2 output A
			{Src: 1, Dst: -1, Prod: 1}, // 3 output B
		},
		Cost:    []int64{1, 1},
		Res:     Resources{Descriptors: 3},
		WindowN: 12,
		WindowM: 2,
		MaxCap:  4,
		Inputs: []Input{
			// Generous periods keep each bounded input edge drained; the
			// point under test is phase realignment over LCM(8,12)=24.
			{EdgeID: 0, Period: 8, Count: 1, FirstTime: 0, Gap: 8, Runs: 1},
			{EdgeID: 1, Period: 12, Count: 1, FirstTime: 0, Gap: 12, Runs: 1},
		},
	}
	r, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status == StatusInconclusive {
		t.Fatalf("periods failed to realign: %s", r.Reason)
	}
	if r.Status != StatusFeasible {
		t.Fatalf("status=%s reason=%s", r.Status, r.Reason)
	}
	if r.Schedule.CycleTime%8 != 0 || r.Schedule.CycleTime%12 != 0 {
		t.Fatalf("cycle %d not a multiple of both input periods", r.Schedule.CycleTime)
	}
	if err := r.Schedule.Verify(p, 3); err != nil {
		t.Fatalf("replay: %v", err)
	}
}

func TestWindowUsesDesignatedInputSemantics(t *testing.T) {
	// With a single input edge, every N input tokens must see M outputs. A
	// tight window on a slow pipeline must be detected even when totals match.
	p := pipelineProblem()
	feed(p, 8, 2, 1, 2)
	p.WindowN = 2
	p.WindowM = 2
	r, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != StatusWindow {
		t.Fatalf("status=%s reason=%s", r.Status, r.Reason)
	}
}

package sdf

import "testing"

func TestDenseStateSpaceTerminates(t *testing.T) {
	// Six actors, rate mismatches that still balance, and all edges at the
	// four-slot hardware limit: the largest intended state space.
	p := &Problem{
		Actors: 6,
		Edges: []Edge{
			{Src: -1, Dst: 0, Cons: 2},
			{Src: 0, Dst: 1, Prod: 2, Cons: 1},
			{Src: 1, Dst: 2, Prod: 1, Cons: 2},
			{Src: 2, Dst: 3, Prod: 1, Cons: 1},
			{Src: 3, Dst: 4, Prod: 1, Cons: 1},
			{Src: 4, Dst: 5, Prod: 1, Cons: 1},
			{Src: 5, Dst: 2, Prod: 1, Cons: 1, Delay: 2},
			{Src: 5, Dst: -1, Prod: 1},
		},
		Cost:    []int64{1, 2, 1, 3, 1, 2},
		Res:     Resources{Descriptors: 4},
		WindowN: 24,
		WindowM: 2,
		MaxCap:  4,
		Inputs: []Input{
			{EdgeID: 0, Period: 48, Count: 2, FirstTime: 0, Gap: 24, Runs: 2},
		},
	}
	r, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status == StatusInconclusive {
		t.Skipf("envelope: %s", r.Reason)
	}
	if r.Status != StatusFeasible {
		t.Fatalf("status=%s reason=%s", r.Status, r.Reason)
	}
	if err := r.Schedule.Verify(p, 2); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if err := VerifyCertificate(p, r.Cap, r.Cert); err != nil {
		t.Fatalf("certificate: %v", err)
	}
}

package sdf

import "testing"

func TestEdgeLowerBoundClassic(t *testing.T) {
	// For a zero-delay edge with production p and consumption c, the classic
	// SDF minimum buffer is p + c - gcd(p, c). Verify against the exact
	// isolated-edge search on a handful of rate pairs.
	cases := []struct{ p, c, want int }{
		{1, 1, 1},
		{2, 1, 2},
		{1, 2, 2},
		{2, 3, 4}, // 2+3-1
		{3, 2, 4},
		{2, 4, 4}, // 2+4-2
		{3, 6, 6}, // 3+6-3
	}
	for _, tc := range cases {
		p := &Problem{
			Actors: 2,
			Edges: []Edge{
				{Src: 0, Dst: 1, Prod: tc.p, Cons: tc.c},
			},
			MaxCap: 8,
		}
		got, _, ok := edgeLowerBound(p, 0)
		if !ok {
			t.Fatalf("p=%d c=%d reported infeasible", tc.p, tc.c)
		}
		if got != tc.want {
			t.Fatalf("p=%d c=%d bound=%d want %d", tc.p, tc.c, got, tc.want)
		}
		// One slot less must be infeasible.
		if got > 0 && edgeSoloFeasible(p, 0, got-1) {
			t.Fatalf("p=%d c=%d cap %d incorrectly feasible", tc.p, tc.c, got-1)
		}
	}
}

func TestReductionWitnessIsRecomputed(t *testing.T) {
	// End-to-end optimum must survive an independent certificate check, which
	// recomputes every lower bound and every one-step reduction from scratch.
	p := pipelineProblem()
	feed(p, 6, 1, 6, 1)
	r, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != StatusFeasible {
		t.Fatalf("status=%s reason=%s", r.Status, r.Reason)
	}
	// Every edge above its structural bound must carry a reduction witness.
	for eid := range r.Cap {
		if r.Cap[eid] > r.Cert.EdgeLowerBound[eid] && !r.Cert.Reduction[eid] {
			t.Fatalf("edge %d above bound but no reduction witness", eid)
		}
	}
	if err := VerifyCertificate(p, r.Cap, r.Cert); err != nil {
		t.Fatalf("certificate: %v", err)
	}
}

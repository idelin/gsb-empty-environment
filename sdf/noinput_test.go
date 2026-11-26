package sdf

import "testing"

func TestNoExternalInputFeedbackRing(t *testing.T) {
	// A self-timed ring with no external source. The feedback edge's delay
	// tokens seed the first firing; outputs leave through an external sink.
	p := &Problem{
		Actors: 2,
		Edges: []Edge{
			{Src: 0, Dst: 1, Prod: 1, Cons: 1, Delay: 1, Cap: 2},
			{Src: 1, Dst: 0, Prod: 1, Cons: 1, Delay: 1, Cap: 2},
			{Src: 0, Dst: -1, Prod: 1},
		},
		Cost:    []int64{2, 3},
		Res:     Resources{Descriptors: 2},
		WindowN: 1,
		WindowM: 0, // no external input: window vacuous
		MaxCap:  4,
	}
	r, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != StatusFeasible {
		t.Fatalf("status=%s reason=%s", r.Status, r.Reason)
	}
	if r.Schedule.CycleOutputs <= 0 {
		t.Fatalf("ring produces no outputs: %+v", r.Schedule)
	}
	if err := r.Schedule.Verify(p, 4); err != nil {
		t.Fatalf("replay: %v", err)
	}
}

func TestNoInputNoDelayDeadlocks(t *testing.T) {
	// Same ring but without the seeding delay: nothing can ever fire.
	p := &Problem{
		Actors: 2,
		Edges: []Edge{
			{Src: 0, Dst: 1, Prod: 1, Cons: 1},
			{Src: 1, Dst: 0, Prod: 1, Cons: 1},
			{Src: 0, Dst: -1, Prod: 1},
		},
		Cost:    []int64{2, 3},
		Res:     Resources{Descriptors: 2},
		WindowN: 1,
		WindowM: 0,
		MaxCap:  4,
	}
	r, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != StatusDeadlock {
		t.Fatalf("status=%s reason=%s", r.Status, r.Reason)
	}
}

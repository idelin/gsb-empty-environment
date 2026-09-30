package main

import "testing"

func testNetwork(t *testing.T) *Network {
	t.Helper()
	in := &Input{
		Name: "test",
		Links: []Link{
			{ID: "AB", From: "A", To: "B", Capacity: 10, Mode: ModeSeparate},
			{ID: "BC", From: "B", To: "C", Capacity: 4, Mode: ModeShared},
			{ID: "AC", From: "A", To: "C", Capacity: 10, Mode: ModeSeparate},
		},
		Flows: []Flow{
			{ID: "F1", Src: "A", Dst: "C", Demand: 5, Path: []string{"AB", "BC"}},
			{ID: "F2", Src: "C", Dst: "A", Demand: 3, Path: []string{"AC"}},
		},
	}
	n, err := BuildNetwork(in)
	if err != nil {
		t.Fatalf("BuildNetwork: %v", err)
	}
	return n
}

func TestResolveRouteDirections(t *testing.T) {
	n := testNetwork(t)
	// F1 traverses AB forward (A->B) then BC forward (B->C).
	if n.Routes[0][0].Dir != DirFwd || n.Routes[0][1].Dir != DirFwd {
		t.Fatalf("F1 route dirs = %+v, want fwd/fwd", n.Routes[0])
	}
	// F2 traverses AC in reverse (C->A).
	if n.Routes[1][0].Dir != DirRev {
		t.Fatalf("F2 route dir = %d, want rev", n.Routes[1][0].Dir)
	}
}

func TestBrokenRouteRejected(t *testing.T) {
	in := &Input{
		Links: []Link{
			{ID: "AB", From: "A", To: "B", Capacity: 1, Mode: ModeSeparate},
			{ID: "CD", From: "C", To: "D", Capacity: 1, Mode: ModeSeparate},
		},
		Flows: []Flow{{ID: "F", Src: "A", Dst: "D", Demand: 1, Path: []string{"AB", "CD"}}},
	}
	if _, err := BuildNetwork(in); err == nil {
		t.Fatal("expected error for non-adjacent route, got nil")
	}
}

func TestRouteNotReachingDstRejected(t *testing.T) {
	in := &Input{
		Links: []Link{{ID: "AB", From: "A", To: "B", Capacity: 1, Mode: ModeSeparate}},
		Flows: []Flow{{ID: "F", Src: "A", Dst: "C", Demand: 1, Path: []string{"AB"}}},
	}
	if _, err := BuildNetwork(in); err == nil {
		t.Fatal("expected error for route ending short of dst, got nil")
	}
}

func TestInvalidInputRejected(t *testing.T) {
	cases := []*Input{
		{Links: []Link{{ID: "L", From: "A", To: "A", Capacity: 1, Mode: ModeSeparate}}},
		{Links: []Link{{ID: "L", From: "A", To: "B", Capacity: 0, Mode: ModeSeparate}}},
		{Links: []Link{{ID: "L", From: "A", To: "B", Capacity: 1, Mode: "bogus"}}},
		{
			Links: []Link{{ID: "L", From: "A", To: "B", Capacity: 1, Mode: ModeSeparate}},
			Flows: []Flow{{ID: "F", Src: "A", Dst: "B", Demand: 1, Path: []string{"NOPE"}}},
		},
		{
			Links:            []Link{{ID: "L", From: "A", To: "B", Capacity: 1, Mode: ModeSeparate}},
			FailureScenarios: []FailureScenario{{ID: "S", FailedLinks: []string{"GHOST"}}},
		},
	}
	for i, in := range cases {
		if _, err := BuildNetwork(in); err == nil {
			t.Fatalf("case %d: expected validation error, got nil", i)
		}
	}
}

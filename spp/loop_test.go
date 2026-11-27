package spp

import "testing"

func TestASPathLoopRejected(t *testing.T) {
	g, err := NewASGraph([]int{0, 1, 2}, []Link{
		{A: 0, B: 1},
		{A: 1, B: 2},
		{A: 0, B: 2},
	})
	if err != nil {
		t.Fatalf("graph: %v", err)
	}
	inst, err := Build(g, PolicyConfig{
		Origin:       0,
		ExplicitOnly: true,
		ExtraPaths: map[int][][]int{
			1: {{1, 2, 1, 0}}, // repeats AS 1: AS_PATH loop
			2: {{2, 1, 0}},
		},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(inst.Ranked[1]) != 0 {
		t.Fatalf("looped explicit path must be rejected, got %+v", inst.Ranked[1])
	}
	foundLoop := false
	for _, v := range inst.Report.Violations {
		if v.Kind == "loop" {
			foundLoop = true
		}
	}
	if !foundLoop {
		t.Fatalf("expected a loop violation, got %+v", inst.Report.Violations)
	}
}

func TestStrictPreferenceOrderIsTotal(t *testing.T) {
	inst := valleyFreeNetwork(t, true, false)
	for _, u := range inst.SortedNodes() {
		seen := map[int]bool{}
		for _, pr := range inst.Ranked[u] {
			if seen[pr.Rank] {
				t.Fatalf("AS %d has duplicate rank %d", u, pr.Rank)
			}
			seen[pr.Rank] = true
		}
		for i := range inst.Ranked[u] {
			if inst.Ranked[u][i].Rank != i {
				t.Fatalf("AS %d ranks are not a dense total order", u)
			}
		}
	}
}

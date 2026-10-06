package treeplan

import (
	"math/rand"
	"strings"
	"testing"
)

// retentionSpec is the canonical reference-retention scenario: serving t1
// pins t1's leaf chunk, so collapsing A cannot free it, and the budget must
// account for the retained chunk while B is expanded.
func retentionSpec(budget int64) *Spec {
	return &Spec{
		Root: &Group{
			Name: "root", ExpandCost: 1, CollapseCost: 1, Memory: 1,
			Groups: []*Group{
				{Name: "A", ExpandCost: 2, CollapseCost: 1, Memory: 4,
					Leaves: []Leaf{{Name: "t1", Memory: 3}}},
				{Name: "B", ExpandCost: 2, CollapseCost: 1, Memory: 4,
					Leaves: []Leaf{{Name: "t2", Memory: 3}}},
			},
		},
		Targets: []string{"t1", "t2"},
		Budget:  budget,
	}
}

func TestSequenceRetentionAndCollapse(t *testing.T) {
	// Budget 11: expand root(1) + A(4+3) = 8, serve t1, collapse A
	// (t1's chunk retained: 1+3=4), expand B (4+4+3=11), serve t2.
	tr := mustBuild(t, retentionSpec(11))
	plan, err := tr.SolveSequence(11)
	if err != nil {
		t.Fatalf("SolveSequence: %v", err)
	}
	if plan.Cost != 6 { // 1+2+0+1+2+0
		t.Fatalf("cost = %d, want 6", plan.Cost)
	}
	if plan.Peak != 11 {
		t.Fatalf("peak = %d, want 11", plan.Peak)
	}
	collapses := 0
	for _, a := range plan.Actions {
		if a.Kind == Collapse {
			collapses++
		}
	}
	if collapses != 1 {
		t.Fatalf("plan %v has %d collapses, want 1", plan.Actions, collapses)
	}
	ledger, err := tr.Replay(plan.Actions, plan.Budget)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if ledger.Cost != plan.Cost || ledger.Peak != plan.Peak {
		t.Fatalf("ledger cost/peak %d/%d != plan %d/%d", ledger.Cost, ledger.Peak, plan.Cost, plan.Peak)
	}
	// The branch collapsed after its target was served must be annotated as
	// retained, with memory 1+3=4 (root chunk + pinned leaf chunk).
	found := false
	for _, e := range ledger.Entries {
		if e.Action.Kind == Collapse {
			found = true
			if e.Memory != 4 {
				t.Fatalf("memory after collapsing %s = %d, want 4 (target retained)", e.Action.Name, e.Memory)
			}
			if !strings.Contains(e.Note, "t1") && !strings.Contains(e.Note, "t2") {
				t.Fatalf("collapse note %q mentions no retained target", e.Note)
			}
			if len(e.Refs) != 1 {
				t.Fatalf("refs after collapse = %v, want exactly one open view", e.Refs)
			}
		}
	}
	if !found {
		t.Fatal("ledger has no collapse")
	}
}

func TestSequenceRetentionInfeasible(t *testing.T) {
	// Without retention, budget 8 would suffice: collapse A frees everything,
	// then expand B needs 1+4+3=8. Because t1's chunk stays referenced, the
	// real minimum budget is 11, so 8 and 10 must be infeasible.
	for _, budget := range []int64{8, 10} {
		tr := mustBuild(t, retentionSpec(budget))
		if _, err := tr.SolveSequence(budget); err == nil {
			t.Fatalf("budget %d: expected infeasible, got a plan", budget)
		}
		if _, ok := tr.BruteForceSequenceCost(budget); ok {
			t.Fatalf("budget %d: brute force found a plan", budget)
		}
	}
}

// Two targets under the same group share its data: the group is expanded
// once and its chunk loaded once.
func TestSequenceSharedSubtreeData(t *testing.T) {
	spec := &Spec{
		Root: &Group{
			Name: "root", ExpandCost: 1, Memory: 0,
			Groups: []*Group{
				{Name: "A", ExpandCost: 3, Memory: 2,
					Leaves: []Leaf{{Name: "t1", Memory: 1}, {Name: "t2", Memory: 1}}},
			},
		},
		Targets: []string{"t1", "t2"},
		Budget:  4,
	}
	tr := mustBuild(t, spec)
	plan, err := tr.SolveSequence(4)
	if err != nil {
		t.Fatalf("SolveSequence: %v", err)
	}
	if plan.Cost != 4 { // expand root 1 + expand A 3, serves free
		t.Fatalf("cost = %d, want 4", plan.Cost)
	}
	if plan.Peak != 4 { // A chunk 2 + two leaf chunks 1+1
		t.Fatalf("peak = %d, want 4", plan.Peak)
	}
	expands := 0
	for _, a := range plan.Actions {
		if a.Kind == Expand && a.Name == "A" {
			expands++
		}
	}
	if expands != 1 {
		t.Fatalf("A expanded %d times, want 1", expands)
	}
}

// Dijkstra and the independent depth-first branch and bound must agree on
// cost and feasibility; every returned plan must replay cleanly.
func TestSequenceMatchesBruteForce(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	for iter := 0; iter < 200; iter++ {
		spec := randomSpec(r, 2+r.Intn(5))
		spec.Budget = int64(r.Intn(26))
		tr := mustBuild(t, spec)
		plan, err := tr.SolveSequence(spec.Budget)
		bfCost, bfOK := tr.BruteForceSequenceCost(spec.Budget)
		if err != nil {
			if bfOK {
				t.Fatalf("iter %d: solver infeasible but brute force found %d", iter, bfCost)
			}
			continue
		}
		if !bfOK {
			t.Fatalf("iter %d: solver found %d but brute force infeasible", iter, plan.Cost)
		}
		if plan.Cost != bfCost {
			t.Fatalf("iter %d: solver cost %d != brute force %d", iter, plan.Cost, bfCost)
		}
		if plan.Peak > spec.Budget {
			t.Fatalf("iter %d: peak %d exceeds budget %d", iter, plan.Peak, spec.Budget)
		}
		ledger, err := tr.Replay(plan.Actions, plan.Budget)
		if err != nil {
			t.Fatalf("iter %d: replay: %v", iter, err)
		}
		if ledger.Cost != plan.Cost || ledger.Peak != plan.Peak {
			t.Fatalf("iter %d: ledger %d/%d != plan %d/%d", iter, ledger.Cost, ledger.Peak, plan.Cost, plan.Peak)
		}
	}
}

// Replay must reject illegal plans.
func TestSequenceReplayRejects(t *testing.T) {
	tr := mustBuild(t, retentionSpec(11))
	good := []Action{
		{Expand, "root"}, {Expand, "A"}, {Serve, "t1"},
		{Collapse, "A"}, {Expand, "B"}, {Serve, "t2"},
	}
	if _, err := tr.Replay(good, 11); err != nil {
		t.Fatalf("good plan rejected: %v", err)
	}
	cases := []struct {
		name    string
		actions []Action
		budget  int64
	}{
		{"child before parent", []Action{{Expand, "A"}}, 11},
		{"double expand", []Action{{Expand, "root"}, {Expand, "root"}}, 11},
		{"collapse unexpanded", []Action{{Expand, "root"}, {Collapse, "A"}}, 11},
		{"collapse with open child", []Action{{Expand, "root"}, {Expand, "A"}, {Collapse, "root"}}, 11},
		{"serve unreachable", []Action{{Expand, "root"}, {Serve, "t1"}}, 11},
		{"serve twice", []Action{{Expand, "root"}, {Expand, "A"}, {Serve, "t1"}, {Serve, "t1"}}, 11},
		{"serve non-target", []Action{{Expand, "root"}, {Serve, "root"}}, 11},
		{"budget exceeded", good, 10},
		{"unserved targets", []Action{{Expand, "root"}, {Expand, "A"}, {Serve, "t1"}}, 11},
	}
	for _, c := range cases {
		if _, err := tr.Replay(c.actions, c.budget); err == nil {
			t.Fatalf("%s: replay accepted bad plan", c.name)
		}
	}
}

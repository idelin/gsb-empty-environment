package destplan_test

import (
	"testing"

	dp "prescreen.local/ddd-9511-20261122-v2"
	"prescreen.local/ddd-9511-20261122-v2/verifier"
)

func basicProblem() *dp.Problem {
	root := &dp.MapNode{
		Kind: dp.NodeBranch,
		Span: dp.Span64(8),
		Children: []dp.Child{
			{Offset: 0, Node: leaf(4, 100, "d1")},
			{Offset: 4, Node: leaf(4, 200, "k2")},
		},
	}
	return &dp.Problem{
		Snapshots: []dp.Snapshot{{ID: "s", Root: root}},
		Graph: graph(
			[]dp.KeyNode{{ID: "R", Role: dp.KeyRoot}, {ID: "d1", Role: dp.KeyData}, {ID: "k2", Role: dp.KeyData}},
			[]dp.WrapEdge{{ID: "e1", From: "R", To: "d1", Cost: 5}, {ID: "e2", From: "R", To: "k2", Cost: 9}},
		),
		Destroy: []dp.Requirement{{Snapshot: "s", Interval: dp.Interval{Lo: 0, Hi: 4}}},
		Retain:  []dp.Requirement{{Snapshot: "s", Interval: dp.Interval{Lo: 4, Hi: 8}}},
	}
}

func TestVerifierRejectsTamperedCost(t *testing.T) {
	p := basicProblem()
	res, err := dp.Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	res.Plan.Cost = 4
	rep, err := verifier.Verify(p, res)
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK {
		t.Fatal("verifier accepted tampered cost")
	}
}

func TestVerifierRejectsTamperedGroups(t *testing.T) {
	p := basicProblem()
	res, err := dp.Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	res.Plan.DisabledGroups = []string{"e2"}
	rep, err := verifier.Verify(p, res)
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK {
		t.Fatal("verifier accepted plan that keeps the destroy key alive")
	}
}

func TestVerifierRejectsForgedLowerBound(t *testing.T) {
	p := basicProblem()
	res, err := dp.Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	res.Plan.Certificate.LowerBound = 1
	rep, err := verifier.Verify(p, res)
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK {
		t.Fatal("verifier accepted forged lower bound")
	}
}

func TestVerifierRejectsForgedInfeasibility(t *testing.T) {
	p := basicProblem()
	forged := &dp.Result{Infeasible: &dp.Infeasibility{Kind: "cut-coupling"}}
	rep, err := verifier.Verify(p, forged)
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK {
		t.Fatal("verifier accepted forged infeasibility on feasible problem")
	}
}

func TestVerifierRejectsBadCouplingWitness(t *testing.T) {
	rootNode := leaf(2, 0, "d")
	retainNode := leaf(2, 100, "r")
	p := &dp.Problem{
		Snapshots: []dp.Snapshot{{ID: "s", Root: rootNode}, {ID: "t", Root: retainNode}},
		Graph: dp.KeyGraph{
			Keys: []dp.KeyNode{
				{ID: "R", Role: dp.KeyRoot},
				{ID: "M", Role: dp.KeyIntermediate},
				{ID: "d", Role: dp.KeyData},
				{ID: "r", Role: dp.KeyData},
			},
			Edges: []dp.WrapEdge{
				{ID: "a", From: "R", To: "M", Cost: 1},
				{ID: "b", From: "R", To: "d", Cost: 2},
				{ID: "md", From: "M", To: "d", Cost: 1},
				{ID: "dr", From: "d", To: "r", Cost: 1},
			},
		},
		Destroy: []dp.Requirement{{Snapshot: "s", Interval: dp.Interval{Lo: 0, Hi: 2}}},
		Retain:  []dp.Requirement{{Snapshot: "t", Interval: dp.Interval{Lo: 0, Hi: 2}}},
	}
	res, err := dp.Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if res.Infeasible == nil {
		t.Fatal("expected coupling infeasibility")
	}
	res.Infeasible.Coupling.MinimalCuts[0].KilledRetain = "d"
	rep, err := verifier.Verify(p, res)
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK {
		t.Fatal("verifier accepted witness naming a non-retain key")
	}
}

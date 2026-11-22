package destplan_test

import (
	"testing"

	dp "prescreen.local/ddd-9511-20261122-v2"
)

func TestEmptyRequestIntervalRejected(t *testing.T) {
	p := basicProblem()
	p.Destroy[0].Interval = dp.Interval{}
	if _, err := dp.Solve(p); err == nil {
		t.Fatal("empty requirement interval should be rejected")
	}
}

func TestFullSpaceLeafCannotShift(t *testing.T) {
	root := &dp.MapNode{Kind: dp.NodeLeaf, Span: dp.SpanFull(), BlockBase: 0, DataKey: "dk"}
	snap := dp.Snapshot{ID: "s", Root: root, Shift: 1}
	p := &dp.Problem{
		Snapshots: []dp.Snapshot{snap},
		Graph: dp.KeyGraph{
			Keys:  []dp.KeyNode{{ID: "R", Role: dp.KeyRoot}, {ID: "dk", Role: dp.KeyData}},
			Edges: []dp.WrapEdge{{ID: "e", From: "R", To: "dk", Cost: 1}},
		},
		Destroy: []dp.Requirement{{Snapshot: "s", Interval: dp.FullSpace()}},
	}
	if _, err := dp.Solve(p); err == nil {
		t.Fatal("shifting a full-space leaf must be rejected")
	}
}

func TestDestroyOverHoleIsVacuous(t *testing.T) {
	root := &dp.MapNode{
		Kind: dp.NodeBranch,
		Span: dp.Span64(8),
		Children: []dp.Child{
			{Offset: 0, Node: hole(4)},
			{Offset: 4, Node: leaf(4, 7, "rk")},
		},
	}
	p := &dp.Problem{
		Snapshots: []dp.Snapshot{{ID: "s", Root: root}},
		Graph: dp.KeyGraph{
			Keys:  []dp.KeyNode{{ID: "R", Role: dp.KeyRoot}, {ID: "rk", Role: dp.KeyData}},
			Edges: []dp.WrapEdge{{ID: "e", From: "R", To: "rk", Cost: 2}},
		},
		Destroy: []dp.Requirement{{Snapshot: "s", Interval: dp.Interval{Lo: 0, Hi: 4}}},
		Retain:  []dp.Requirement{{Snapshot: "s", Interval: dp.Interval{Lo: 4, Hi: 8}}},
	}
	res, err := dp.Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if res.Infeasible != nil {
		t.Fatalf("destroy over a hole needs no edge cut: %+v", res.Infeasible)
	}
	if len(res.Plan.DisabledGroups) != 0 || res.Plan.Cost != 0 {
		t.Fatalf("vacuous destroy should cost zero, got %v", res.Plan)
	}
	mustVerify(t, p, res)
}

func TestGapReportedOnResolve(t *testing.T) {
	root := &dp.MapNode{
		Kind: dp.NodeBranch,
		Span: dp.Span64(8),
		Children: []dp.Child{
			{Offset: 0, Node: leaf(4, 0, "rk")},
			{Offset: 4, Node: hole(4)},
		},
	}
	res, err := dp.Resolve(dp.Snapshot{ID: "s", Root: root}, dp.Interval{Lo: 0, Hi: 8})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Gaps) != 1 || !res.Gaps[0].Hole {
		t.Fatalf("want one hole gap, got %+v", res.Gaps)
	}
	if len(res.Pieces) != 1 {
		t.Fatalf("want one piece, got %d", len(res.Pieces))
	}
}

func TestRejectOverlappingBranchChildren(t *testing.T) {
	n := &dp.MapNode{
		Kind: dp.NodeBranch,
		Span: dp.Span64(8),
		Children: []dp.Child{
			{Offset: 0, Node: hole(5)},
			{Offset: 4, Node: hole(4)},
		},
	}
	p := &dp.Problem{
		Snapshots: []dp.Snapshot{{ID: "s", Root: n}},
		Graph:     dp.KeyGraph{Keys: []dp.KeyNode{{ID: "R", Role: dp.KeyRoot}}},
	}
	if _, err := dp.Solve(p); err == nil {
		t.Fatal("overlapping branch children should be rejected")
	}
}

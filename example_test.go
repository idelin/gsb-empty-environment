package destplan_test

import (
	"fmt"

	dp "prescreen.local/ddd-9511-20261122-v2"
)

func ExampleSolve() {
	root := &dp.MapNode{
		Kind: dp.NodeBranch,
		Span: dp.Span64(8),
		Children: []dp.Child{
			{Offset: 0, Node: &dp.MapNode{Kind: dp.NodeLeaf, Span: dp.Span64(4), BlockBase: 100, DataKey: "secret"}},
			{Offset: 4, Node: &dp.MapNode{Kind: dp.NodeLeaf, Span: dp.Span64(4), BlockBase: 200, DataKey: "retained"}},
		},
	}
	problem := &dp.Problem{
		Snapshots: []dp.Snapshot{{ID: "snap-1", Root: root}},
		Graph: dp.KeyGraph{
			Keys: []dp.KeyNode{
				{ID: "root", Role: dp.KeyRoot},
				{ID: "secret", Role: dp.KeyData},
				{ID: "retained", Role: dp.KeyData},
			},
			Edges: []dp.WrapEdge{
				{ID: "wrap-secret", From: "root", To: "secret", Cost: 3},
				{ID: "wrap-retained", From: "root", To: "retained", Cost: 7},
			},
		},
		Destroy: []dp.Requirement{{Snapshot: "snap-1", Interval: dp.Interval{Lo: 0, Hi: 4}}},
		Retain:  []dp.Requirement{{Snapshot: "snap-1", Interval: dp.Interval{Lo: 4, Hi: 8}}},
	}
	result, err := dp.Solve(problem)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	if result.Infeasible != nil {
		fmt.Println("infeasible:", result.Infeasible.Kind)
		return
	}
	fmt.Println("disable:", result.Plan.DisabledGroups, "cost:", result.Plan.Cost)
	fmt.Println("lower bound:", result.Plan.Certificate.LowerBound)
	// Output:
	// disable: [wrap-secret] cost: 3
	// lower bound: 3
}

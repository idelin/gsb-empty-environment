package destplan_test

import (
	"strings"
	"testing"

	dp "prescreen.local/ddd-9511-20261122-v2"
)

func TestRejectInconsistentAliasCost(t *testing.T) {
	p := basicProblem()
	p.Graph.Edges = append(p.Graph.Edges, dp.WrapEdge{ID: "e1", From: "R", To: "d1", Cost: 99})
	if _, err := dp.Solve(p); err == nil || !strings.Contains(err.Error(), "inconsistent costs") {
		t.Fatalf("want inconsistent-cost error, got %v", err)
	}
}

func TestRejectTooManyGroups(t *testing.T) {
	keys := []dp.KeyNode{{ID: "R", Role: dp.KeyRoot}, {ID: "d", Role: dp.KeyData}}
	edges := make([]dp.WrapEdge, 0, 21)
	for i := 0; i < 21; i++ {
		edges = append(edges, dp.WrapEdge{
			ID:   edgeLabel(i),
			From: "R",
			To:   "d",
			Cost: 1,
		})
	}
	p := &dp.Problem{
		Snapshots: []dp.Snapshot{{ID: "s", Root: leaf(1, 0, "d")}},
		Graph:     dp.KeyGraph{Keys: keys, Edges: edges},
		Destroy:   []dp.Requirement{{Snapshot: "s", Interval: dp.Interval{Lo: 0, Hi: 1}}},
	}
	if _, err := dp.Solve(p); err == nil || !strings.Contains(err.Error(), "at most 20") {
		t.Fatalf("want group-limit error, got %v", err)
	}
}

func edgeLabel(i int) string {
	return "g" + string(rune('a'+i/10)) + string(rune('a'+i%10))
}

func TestTransitiveAliasMerging(t *testing.T) {
	root := leaf(2, 0, "d")
	p := &dp.Problem{
		Snapshots: []dp.Snapshot{{ID: "s", Root: root}},
		Graph: dp.KeyGraph{
			Keys: []dp.KeyNode{{ID: "R", Role: dp.KeyRoot}, {ID: "d", Role: dp.KeyData}},
			Edges: []dp.WrapEdge{
				{ID: "e1", From: "R", To: "d", Cost: 4, Aliases: []string{"shared"}},
				{ID: "e2", From: "R", To: "d", Cost: 4, Aliases: []string{"shared"}},
			},
		},
		Destroy: []dp.Requirement{{Snapshot: "s", Interval: dp.Interval{Lo: 0, Hi: 2}}},
	}
	res, err := dp.Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Plan.DisabledGroups) != 1 {
		t.Fatalf("transitive aliases should merge into one group, got %v", res.Plan.DisabledGroups)
	}
	want := map[string]bool{"e1": true, "e2": true, "shared": true}
	if len(res.Plan.DisabledAliases) != len(want) {
		t.Fatalf("aliases = %v, want e1,e2,shared", res.Plan.DisabledAliases)
	}
	for _, a := range res.Plan.DisabledAliases {
		if !want[a] {
			t.Fatalf("unexpected alias %q", a)
		}
	}
	if res.Plan.Cost != 4 {
		t.Fatalf("merged group cost counted once, got %d", res.Plan.Cost)
	}
	mustVerify(t, p, res)
}

func TestRejectUnknownSnapshot(t *testing.T) {
	p := basicProblem()
	p.Destroy[0].Snapshot = "ghost"
	if _, err := dp.Solve(p); err == nil || !strings.Contains(err.Error(), "unknown snapshot") {
		t.Fatalf("want unknown-snapshot error, got %v", err)
	}
}

func TestRejectCycleInMappingTree(t *testing.T) {
	n := &dp.MapNode{Kind: dp.NodeHole, Span: dp.Span64(4)}
	n.Kind = dp.NodeBranch
	n.Children = []dp.Child{{Offset: 0, Node: n}}
	p := &dp.Problem{
		Snapshots: []dp.Snapshot{{ID: "s", Root: n}},
		Graph: dp.KeyGraph{
			Keys: []dp.KeyNode{{ID: "R", Role: dp.KeyRoot}},
		},
	}
	if _, err := dp.Solve(p); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("want cycle error, got %v", err)
	}
}

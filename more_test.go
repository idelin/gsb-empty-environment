package destplan_test

import (
	"math"
	"testing"

	dp "prescreen.local/ddd-9511-20261122-v2"
	"prescreen.local/ddd-9511-20261122-v2/verifier"
)

// TestCutCoupling constructs two parallel root paths that merge at a shared
// destroy key, through which the retain key is also reachable. Every cut
// that kills the destroy key necessarily severs every retain path.
func TestCutCoupling(t *testing.T) {
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
	if res.Infeasible == nil || res.Infeasible.Kind != "cut-coupling" {
		t.Fatalf("want cut-coupling, got %+v", res.Infeasible)
	}
	if len(res.Infeasible.Coupling.MinimalCuts) == 0 {
		t.Fatal("coupling proof has no witnesses")
	}
	for _, w := range res.Infeasible.Coupling.MinimalCuts {
		if w.KilledRetain != "r" {
			t.Fatalf("witness killed %q, want r", w.KilledRetain)
		}
	}
	mustVerify(t, p, res)
}

// TestHighBaseLeaf places a leaf whose last block identity is MaxUint64.
func TestHighBaseLeaf(t *testing.T) {
	root := &dp.MapNode{
		Kind:      dp.NodeLeaf,
		Span:      dp.Span64(4),
		BlockBase: math.MaxUint64 - 3,
		DataKey:   "dk",
	}
	p := &dp.Problem{
		Snapshots: []dp.Snapshot{{ID: "s", Root: root}},
		Graph: dp.KeyGraph{
			Keys:  []dp.KeyNode{{ID: "R", Role: dp.KeyRoot}, {ID: "dk", Role: dp.KeyData}},
			Edges: []dp.WrapEdge{{ID: "e", From: "R", To: "dk", Cost: 1}},
		},
		Destroy: []dp.Requirement{{Snapshot: "s", Interval: dp.Interval{Lo: 0, Hi: 4}}},
	}
	res, err := dp.Solve(p)
	if err != nil {
		t.Fatalf("high-base leaf rejected: %v", err)
	}
	if res.Plan == nil || res.Plan.Cost != 1 {
		t.Fatalf("unexpected result %+v", res)
	}
	mustVerify(t, p, res)
}

// TestSparseHolesAndTranslation checks symbolic resolution over shift nodes,
// sparse holes and interval translation without per-sector expansion.
func TestSparseHolesAndTranslation(t *testing.T) {
	inner := &dp.MapNode{
		Kind: dp.NodeBranch,
		Span: dp.Span64(16),
		Children: []dp.Child{
			{Offset: 0, Node: leaf(8, 10, "dk")},
			{Offset: 8, Node: hole(8)},
		},
	}
	root := &dp.MapNode{
		Kind: dp.NodeBranch,
		Span: dp.Span64(32),
		Children: []dp.Child{
			{Offset: 0, Node: &dp.MapNode{Kind: dp.NodeShift, Span: dp.Span64(16), Inner: inner, Delta: 4}},
			{Offset: 16, Node: leaf(16, 100, "rk")},
		},
	}
	p := &dp.Problem{
		Snapshots: []dp.Snapshot{{ID: "s", Root: root}},
		Graph: dp.KeyGraph{
			Keys: []dp.KeyNode{
				{ID: "R", Role: dp.KeyRoot},
				{ID: "dk", Role: dp.KeyData},
				{ID: "rk", Role: dp.KeyData},
			},
			Edges: []dp.WrapEdge{
				{ID: "ed", From: "R", To: "dk", Cost: 2},
				{ID: "er", From: "R", To: "rk", Cost: 7},
			},
		},
		Destroy: []dp.Requirement{{Snapshot: "s", Interval: dp.Interval{Lo: 4, Hi: 12}}},
		Retain:  []dp.Requirement{{Snapshot: "s", Interval: dp.Interval{Lo: 16, Hi: 32}}},
	}
	res, err := dp.Resolve(p.Snapshots[0], dp.Interval{Lo: 0, Hi: 32})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Pieces) != 2 {
		t.Fatalf("pieces = %d, want 2 (one shifted leaf, one plain leaf)", len(res.Pieces))
	}
	if len(res.Gaps) != 1 || !res.Gaps[0].Hole {
		t.Fatalf("gaps = %+v, want one sparse hole", res.Gaps)
	}
	out, err := dp.Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if out.Infeasible != nil {
		t.Fatalf("unexpected infeasibility: %+v", out.Infeasible)
	}
	if len(out.Plan.DisabledGroups) != 1 || out.Plan.DisabledGroups[0] != "ed" {
		t.Fatalf("plan = %v, want {ed}", out.Plan.DisabledGroups)
	}
	rep, err := verifier.Verify(p, out)
	if err != nil || !rep.OK {
		t.Fatalf("verifier: %+v err=%v", rep, err)
	}
}

// TestFullSpaceBoundary exercises the 2^64 exclusive end and a leaf spanning
// the whole logical space without any sector enumeration.
func TestFullSpaceBoundary(t *testing.T) {
	root := &dp.MapNode{
		Kind:      dp.NodeLeaf,
		Span:      dp.SpanFull(),
		BlockBase: 0,
		DataKey:   "dk",
	}
	p := &dp.Problem{
		Snapshots: []dp.Snapshot{{ID: "s", Root: root}},
		Graph: dp.KeyGraph{
			Keys:  []dp.KeyNode{{ID: "R", Role: dp.KeyRoot}, {ID: "dk", Role: dp.KeyData}},
			Edges: []dp.WrapEdge{{ID: "e", From: "R", To: "dk", Cost: 4}},
		},
		Destroy: []dp.Requirement{{Snapshot: "s", Interval: dp.FullSpace()}},
	}
	res, err := dp.Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if res.Infeasible != nil || res.Plan.Cost != 4 {
		t.Fatalf("unexpected result: %+v", res)
	}
	mustVerify(t, p, res)
}

// TestTwentyEdgePerformance enumerates 2^20 subsets and verifies the run
// stays in the intended exhaustive regime and returns a plan with a valid
// lower bound.
func TestTwentyEdgePerformance(t *testing.T) {
	keys := []dp.KeyNode{{ID: "R", Role: dp.KeyRoot}, {ID: "d", Role: dp.KeyData}}
	edges := make([]dp.WrapEdge, 0, 20)
	for i := 0; i < 19; i++ {
		keys = append(keys, dp.KeyNode{ID: edgeName(i), Role: dp.KeyData})
		edges = append(edges, dp.WrapEdge{ID: edgeName(i), From: "R", To: edgeName(i), Cost: 1})
	}
	edges = append(edges, dp.WrapEdge{ID: "zz-final", From: "R", To: "d", Cost: 1})
	p := &dp.Problem{
		Snapshots: []dp.Snapshot{{ID: "s", Root: leaf(1, 0, "d")}},
		Graph:     dp.KeyGraph{Keys: keys, Edges: edges},
		Destroy:   []dp.Requirement{{Snapshot: "s", Interval: dp.Interval{Lo: 0, Hi: 1}}},
	}
	res, err := dp.Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if res.Plan.Certificate.Subsets != 1<<20 {
		t.Fatalf("subsets = %d, want %d", res.Plan.Certificate.Subsets, 1<<20)
	}
	if res.Plan.Certificate.LowerBound != 1 {
		t.Fatalf("lower bound = %d, want 1", res.Plan.Certificate.LowerBound)
	}
	mustVerify(t, p, res)
}

func edgeName(i int) string {
	return "e" + string(rune('a'+i))
}

// TestRetainUnreachable covers a retain key whose only wrapping path is
// absent even before any edge is deactivated.
func TestRetainUnreachable(t *testing.T) {
	p := &dp.Problem{
		Snapshots: []dp.Snapshot{{ID: "s", Root: leaf(2, 0, "r")}},
		Graph: dp.KeyGraph{
			Keys:  []dp.KeyNode{{ID: "R", Role: dp.KeyRoot}, {ID: "r", Role: dp.KeyData}},
			Edges: []dp.WrapEdge{{ID: "e", From: "r", To: "R", Cost: 1}},
		},
		Retain: []dp.Requirement{{Snapshot: "s", Interval: dp.Interval{Lo: 0, Hi: 2}}},
	}
	res, err := dp.Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if res.Infeasible == nil || res.Infeasible.Kind != "retain-unreachable" {
		t.Fatalf("want retain-unreachable, got %+v", res.Infeasible)
	}
	mustVerify(t, p, res)
}

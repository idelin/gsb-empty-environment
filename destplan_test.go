package destplan_test

import (
	"testing"

	dp "prescreen.local/ddd-9511-20261122-v2"
	"prescreen.local/ddd-9511-20261122-v2/verifier"
)

func leaf(span uint64, blockBase uint64, key string) *dp.MapNode {
	return &dp.MapNode{Kind: dp.NodeLeaf, Span: dp.Span64(span), BlockBase: blockBase, DataKey: key}
}

func hole(span uint64) *dp.MapNode {
	return &dp.MapNode{Kind: dp.NodeHole, Span: dp.Span64(span)}
}

func graph(keys []dp.KeyNode, edges []dp.WrapEdge) dp.KeyGraph {
	return dp.KeyGraph{Keys: keys, Edges: edges}
}

func mustVerify(t *testing.T, p *dp.Problem, res *dp.Result) *verifier.Report {
	t.Helper()
	rep, err := verifier.Verify(p, res)
	if err != nil {
		t.Fatalf("verifier error: %v", err)
	}
	if !rep.OK {
		t.Fatalf("verifier rejected result: %s", rep.Reason)
	}
	return rep
}

// TestBasicDestroyRetain builds one snapshot whose two leaves are wrapped
// through separate edges: destroying one sector range must cut only its edge.
func TestBasicDestroyRetain(t *testing.T) {
	root := &dp.MapNode{
		Kind: dp.NodeBranch,
		Span: dp.Span64(8),
		Children: []dp.Child{
			{Offset: 0, Node: leaf(4, 100, "d1")},
			{Offset: 4, Node: leaf(4, 200, "k2")},
		},
	}
	p := &dp.Problem{
		Snapshots: []dp.Snapshot{{ID: "s", Root: root}},
		Graph: graph(
			[]dp.KeyNode{{ID: "R", Role: dp.KeyRoot}, {ID: "d1", Role: dp.KeyData}, {ID: "k2", Role: dp.KeyData}},
			[]dp.WrapEdge{
				{ID: "e1", From: "R", To: "d1", Cost: 5},
				{ID: "e2", From: "R", To: "k2", Cost: 9},
			},
		),
		Destroy: []dp.Requirement{{Snapshot: "s", Interval: dp.Interval{Lo: 0, Hi: 4}}},
		Retain:  []dp.Requirement{{Snapshot: "s", Interval: dp.Interval{Lo: 4, Hi: 8}}},
	}
	res, err := dp.Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if res.Infeasible != nil {
		t.Fatalf("unexpected infeasibility: %+v", res.Infeasible)
	}
	if got := res.Plan.DisabledGroups; len(got) != 1 || got[0] != "e1" || res.Plan.Cost != 5 {
		t.Fatalf("unexpected plan %+v", res.Plan)
	}
	if len(res.Plan.DisabledAliases) != 1 || res.Plan.DisabledAliases[0] != "e1" {
		t.Fatalf("unexpected aliases %v", res.Plan.DisabledAliases)
	}
	mustVerify(t, p, res)
}

// TestAliasAtomicity deactivates an edge with several aliases: all aliases
// must appear in the plan and cost must be counted once.
func TestAliasAtomicity(t *testing.T) {
	root := &dp.MapNode{
		Kind:     dp.NodeBranch,
		Span:     dp.Span64(8),
		Children: []dp.Child{{Offset: 0, Node: leaf(4, 0, "da")}, {Offset: 4, Node: leaf(4, 8, "kb")}},
	}
	p := &dp.Problem{
		Snapshots: []dp.Snapshot{{ID: "s", Root: root}},
		Graph: graph(
			[]dp.KeyNode{{ID: "R", Role: dp.KeyRoot}, {ID: "da", Role: dp.KeyData}, {ID: "kb", Role: dp.KeyData}},
			[]dp.WrapEdge{
				{ID: "z-edge", From: "R", To: "da", Cost: 3, Aliases: []string{"alias-1", "alias-2"}},
				{ID: "keep", From: "R", To: "kb", Cost: 1},
			},
		),
		Destroy: []dp.Requirement{{Snapshot: "s", Interval: dp.Interval{Lo: 0, Hi: 4}}},
		Retain:  []dp.Requirement{{Snapshot: "s", Interval: dp.Interval{Lo: 4, Hi: 8}}},
	}
	res, err := dp.Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	wantAliases := []string{"alias-1", "alias-2", "z-edge"}
	if len(res.Plan.DisabledAliases) != len(wantAliases) {
		t.Fatalf("aliases = %v, want %v", res.Plan.DisabledAliases, wantAliases)
	}
	for i, w := range wantAliases {
		if res.Plan.DisabledAliases[i] != w {
			t.Fatalf("aliases = %v, want %v", res.Plan.DisabledAliases, wantAliases)
		}
	}
	if res.Plan.Cost != 3 {
		t.Fatalf("cost = %d, want 3 (group counted once)", res.Plan.Cost)
	}
	mustVerify(t, p, res)
}

// TestZeroCostEdgeTieBreak exercises zero-cost edges and lexicographic
// tie-breaking between equal-cost, same-cardinality plans.
func TestZeroCostEdgeTieBreak(t *testing.T) {
	root := leaf(2, 0, "d")
	p := &dp.Problem{
		Snapshots: []dp.Snapshot{{ID: "s", Root: root}},
		Graph: graph(
			[]dp.KeyNode{
				{ID: "R", Role: dp.KeyRoot},
				{ID: "m", Role: dp.KeyIntermediate},
				{ID: "n", Role: dp.KeyIntermediate},
				{ID: "d", Role: dp.KeyData},
			},
			[]dp.WrapEdge{
				{ID: "a", From: "R", To: "m", Cost: 0},
				{ID: "b", From: "m", To: "d", Cost: 0},
				{ID: "c", From: "R", To: "n", Cost: 0},
				{ID: "e", From: "n", To: "d", Cost: 0},
			},
		),
		Destroy: []dp.Requirement{{Snapshot: "s", Interval: dp.Interval{Lo: 0, Hi: 2}}},
	}
	res, err := dp.Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if res.Plan.Cost != 0 || len(res.Plan.DisabledGroups) != 2 ||
		res.Plan.DisabledGroups[0] != "a" || res.Plan.DisabledGroups[1] != "c" {
		t.Fatalf("want lexicographically smallest zero-cost path cut {a c}, got %v cost %d",
			res.Plan.DisabledGroups, res.Plan.Cost)
	}
	mustVerify(t, p, res)
}

// TestDeletionRevoked models a delete that was undone: the same logical range
// is destroyed in an older snapshot but retained in a newer snapshot whose
// leaf points at the same ciphertext block. That must be infeasible.
func TestDeletionRevoked(t *testing.T) {
	oldLeaf := leaf(4, 50, "shared")
	newLeaf := leaf(4, 50, "shared")
	p := &dp.Problem{
		Snapshots: []dp.Snapshot{
			{ID: "old", Root: oldLeaf},
			{ID: "new", Root: newLeaf},
		},
		Graph: graph(
			[]dp.KeyNode{{ID: "R", Role: dp.KeyRoot}, {ID: "shared", Role: dp.KeyData}},
			[]dp.WrapEdge{{ID: "e", From: "R", To: "shared", Cost: 1}},
		),
		Destroy: []dp.Requirement{{Snapshot: "old", Interval: dp.Interval{Lo: 0, Hi: 4}}},
		Retain:  []dp.Requirement{{Snapshot: "new", Interval: dp.Interval{Lo: 0, Hi: 4}}},
	}
	res, err := dp.Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if res.Infeasible == nil {
		t.Fatal("expected infeasibility for revoked deletion")
	}
	if res.Infeasible.Kind != "key-conflict" {
		t.Fatalf("kind = %q, want key-conflict", res.Infeasible.Kind)
	}
	mustVerify(t, p, res)
}

// TestSameBlockDifferentCoordinate keeps distinct data keys but reuses one
// ciphertext block identity under a different coordinate in another snapshot.
func TestSameBlockDifferentCoordinate(t *testing.T) {
	snapA := leaf(2, 70, "dk")
	snapB := &dp.MapNode{Kind: dp.NodeLeaf, Span: dp.Span64(2), BlockBase: 70, CoordBase: 999, DataKey: "rk"}
	p := &dp.Problem{
		Snapshots: []dp.Snapshot{{ID: "a", Root: snapA}, {ID: "b", Root: snapB}},
		Graph: graph(
			[]dp.KeyNode{{ID: "R", Role: dp.KeyRoot}, {ID: "dk", Role: dp.KeyData}, {ID: "rk", Role: dp.KeyData}},
			[]dp.WrapEdge{{ID: "ed", From: "R", To: "dk", Cost: 1}, {ID: "er", From: "R", To: "rk", Cost: 1}},
		),
		Destroy: []dp.Requirement{{Snapshot: "a", Interval: dp.Interval{Lo: 0, Hi: 2}}},
		Retain:  []dp.Requirement{{Snapshot: "b", Interval: dp.Interval{Lo: 0, Hi: 2}}},
	}
	res, err := dp.Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if res.Infeasible == nil || res.Infeasible.Kind != "block-conflict" {
		t.Fatalf("want block-conflict, got %+v", res.Infeasible)
	}
	mustVerify(t, p, res)
}

package netsec

import "testing"

func TestEnumerationExhaustedMidSearch(t *testing.T) {
	events := []Event{
		evt("u1", "f", 1, KindUse, "c", 1, 4),
		evt("r1", "g", 1, KindRevoke, "c", 1, 4),
	}
	g, _, err := buildConstraintGraph(events)
	if err != nil {
		t.Fatalf("buildConstraintGraph: %v", err)
	}
	pairs := []pairIndices{{use: 0, revoke: 1}}

	// Domain size is 16 (under the pre-check cap), but a budget of 3 must
	// interrupt backtracking partway and report a skip.
	counts, skipped := enumerateGroup(g, pairs, Options{MaxEnumNodes: 8, MaxEnumAssignments: 3})
	if !skipped || counts != nil {
		t.Fatalf("mid-search budget exhaustion must skip, got counts=%v skipped=%v", counts, skipped)
	}
}

func TestVerdictFromCountsUnknown(t *testing.T) {
	if verdictFromCounts(enumCounts{}) != VerdictUnknown {
		t.Fatalf("zero counts must map to unknown")
	}
}

func TestValidateEmptyID(t *testing.T) {
	events := []Event{
		{ID: "", Feed: "f", Seq: 1, Kind: KindUse, Cred: "c", Lo: 1, Hi: 2},
	}
	if _, err := Correlate(events); err == nil {
		t.Fatalf("empty id must be rejected")
	}
}

func TestTopoOrderDetectsCycle(t *testing.T) {
	g := &constraintGraph{nodes: []*node{
		{pred: []int{1}, succ: []int{1}, feasible: true},
		{pred: []int{0}, succ: []int{0}, feasible: true},
	}}
	if _, ok := g.topoOrder(); ok {
		t.Fatalf("cyclic graph must fail topological ordering")
	}
}

func TestCanReachTransitiveAndMiss(t *testing.T) {
	g := &constraintGraph{nodes: []*node{
		{succ: []int{1}, feasible: true},
		{succ: []int{2}, feasible: true},
		{feasible: true},
		{feasible: true},
	}}
	if !g.canReach(0, 2) {
		t.Fatalf("0 -> 1 -> 2 must be reachable")
	}
	if g.canReach(0, 3) || g.canReach(0, 0) {
		t.Fatalf("unconnected nodes and self lookups must not reach")
	}
}

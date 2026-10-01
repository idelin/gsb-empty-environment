package main

import (
	"strings"
	"testing"
)

func testScenario() *Scenario {
	return &Scenario{
		Links: []Link{
			{ID: "L1", From: "A", To: "B", Capacity: 10, Mode: ModeShared},
			{ID: "L2", From: "B", To: "C", Capacity: 8, Mode: ModeSeparate},
			{ID: "L3", From: "A", To: "D", Capacity: 6, Mode: ModeUnidirectional},
			{ID: "L4", From: "D", To: "C", Capacity: 6, Mode: ModeShared},
		},
		Flows: []Flow{
			{ID: "F1", Src: "A", Dst: "C", Demand: 5, Route: []string{"A", "B", "C"}},
			{ID: "F2", Src: "B", Dst: "C", Demand: 4, Route: []string{"B", "C"}},
			{ID: "F3", Src: "C", Dst: "A", Demand: 3, Route: []string{"C", "B", "A"}},
			{ID: "F4", Src: "A", Dst: "C", Demand: 4, Route: []string{"A", "D", "C"}},
		},
		Failures: []Failure{
			{Name: "cut-L1", Links: []string{"L1"}},
		},
	}
}

func resolve(t *testing.T, sc *Scenario, failed map[string]bool) []resolvedFlow {
	t.Helper()
	if err := sc.validate(); err != nil {
		t.Fatalf("invalid scenario: %v", err)
	}
	return resolveFlows(sc, buildTopology(sc, failed))
}

func loadOf(t *testing.T, loads map[string]*usageLoad, key string) float64 {
	t.Helper()
	u, ok := loads[key]
	if !ok {
		t.Fatalf("no usage load for key %q", key)
	}
	return u.load
}

func TestSharedBidirectionalAccumulates(t *testing.T) {
	sc := testScenario()
	loads := computeLoads(resolve(t, sc, nil))
	// L1 shared: F1 forward (5) + F3 reverse (3) share one budget.
	if got := loadOf(t, loads, "L1|shared"); got != 8 {
		t.Fatalf("L1 shared load = %v, want 8", got)
	}
}

func TestSeparateBidirectionalDoesNotAccumulate(t *testing.T) {
	sc := testScenario()
	loads := computeLoads(resolve(t, sc, nil))
	// L2 separate: F1+F2 forward = 9, F3 reverse = 3, independent budgets.
	if got := loadOf(t, loads, "L2|fwd"); got != 9 {
		t.Fatalf("L2 fwd load = %v, want 9", got)
	}
	if got := loadOf(t, loads, "L2|rev"); got != 3 {
		t.Fatalf("L2 rev load = %v, want 3", got)
	}
	if !loads["L2|fwd"].overloaded() {
		t.Fatal("L2 fwd should be overloaded (9 > 8)")
	}
	if loads["L2|rev"].overloaded() {
		t.Fatal("L2 rev should not be overloaded (3 <= 8)")
	}
}

func TestUnreachableFlowNotZeroLoadSuccess(t *testing.T) {
	sc := testScenario()
	// F5 routes over a nonexistent link X->Y.
	sc.Flows = append(sc.Flows, Flow{ID: "F5", Src: "X", Dst: "Y", Demand: 2, Route: []string{"X", "Y"}})
	resolved := resolve(t, sc, nil)
	var f5 *resolvedFlow
	for i := range resolved {
		if resolved[i].flow.ID == "F5" {
			f5 = &resolved[i]
		}
	}
	if f5 == nil || !f5.unreachable {
		t.Fatal("F5 must be flagged unreachable")
	}
	loads := computeLoads(resolved)
	for k, u := range loads {
		for _, fi := range u.flows {
			if resolved[fi].flow.ID == "F5" {
				t.Fatalf("unreachable flow F5 counted in load of %s", k)
			}
		}
	}
	report := Validate(sc)
	if !strings.Contains(report, "UNREACHABLE flow F5") {
		t.Fatal("report must list F5 as unreachable, not as zero-load success")
	}
}

func TestFailureScenario(t *testing.T) {
	sc := testScenario()
	failed := map[string]bool{"L1": true}
	resolved := resolve(t, sc, failed)
	unreachable := map[string]bool{}
	for _, rf := range resolved {
		if rf.unreachable {
			unreachable[rf.flow.ID] = true
		}
	}
	// F1 and F3 use L1 and must become unreachable; F2, F4 survive.
	if !unreachable["F1"] || !unreachable["F3"] {
		t.Fatalf("F1 and F3 should be unreachable after cut-L1, got %v", unreachable)
	}
	if unreachable["F2"] || unreachable["F4"] {
		t.Fatalf("F2 and F4 should stay reachable, got %v", unreachable)
	}
	loads := computeLoads(resolved)
	if got := loadOf(t, loads, "L2|fwd"); got != 4 {
		t.Fatalf("after cut-L1, L2 fwd load = %v, want 4 (only F2)", got)
	}
	if loads["L2|fwd"].overloaded() {
		t.Fatal("L2 fwd should not be overloaded after cut-L1")
	}
}

func TestMinimalRemovalSet(t *testing.T) {
	sc := testScenario()
	resolved := resolve(t, sc, nil)
	loads := computeLoads(resolved)
	u := loads["L2|fwd"] // load 9, cap 8, excess 1, flows F1(5) F2(4)
	rm := minimalRemovalSet(resolved, u)
	if len(rm) != 1 || rm[0] != "F2" {
		t.Fatalf("minimal removal set = %v, want [F2] (smallest single flow covering excess)", rm)
	}
}

func TestMinimalRemovalSetMultiFlow(t *testing.T) {
	// cap 5, flows 2+2+4 = 8, excess 3: no single flow of exactly 3;
	// cheapest single flow covering excess is 4 -> {F3}.
	sc := &Scenario{
		Links: []Link{{ID: "L", From: "A", To: "B", Capacity: 5, Mode: ModeUnidirectional}},
		Flows: []Flow{
			{ID: "F1", Src: "A", Dst: "B", Demand: 2, Route: []string{"A", "B"}},
			{ID: "F2", Src: "A", Dst: "B", Demand: 2, Route: []string{"A", "B"}},
			{ID: "F3", Src: "A", Dst: "B", Demand: 4, Route: []string{"A", "B"}},
		},
	}
	resolved := resolve(t, sc, nil)
	loads := computeLoads(resolved)
	rm := minimalRemovalSet(resolved, loads["L|fwd"])
	if len(rm) != 1 || rm[0] != "F3" {
		t.Fatalf("minimal removal set = %v, want [F3]", rm)
	}
	// cap 5, flows 2+2+3 = 7, excess 2: single flow of demand 2 suffices -> {F1}.
	sc.Links[0].Capacity = 5
	sc.Flows[2].Demand = 3
	resolved = resolve(t, sc, nil)
	loads = computeLoads(resolved)
	rm = minimalRemovalSet(resolved, loads["L|fwd"])
	if len(rm) != 1 || rm[0] != "F1" {
		t.Fatalf("minimal removal set = %v, want [F1]", rm)
	}
}

func TestRedundancyCondition(t *testing.T) {
	sc := testScenario()
	topo := buildTopology(sc, nil)
	// F1 primary links {L1,L2}; backup A-D-C via L3,L4 exists.
	alt := findAlternatePath(topo, "A", "C", map[string]bool{"L1": true, "L2": true})
	if alt == nil || strings.Join(alt, "-") != "A-D-C" {
		t.Fatalf("F1 backup path = %v, want [A D C]", alt)
	}
	// F4 primary links {L3,L4}; backup A-B-C via L1,L2 exists.
	alt = findAlternatePath(topo, "A", "C", map[string]bool{"L3": true, "L4": true})
	if alt == nil || strings.Join(alt, "-") != "A-B-C" {
		t.Fatalf("F4 backup path = %v, want [A B C]", alt)
	}
	// Remove L3 and L4 from topology: F1 has no backup left.
	sc2 := testScenario()
	sc2.Links = sc2.Links[:2]
	topo2 := buildTopology(sc2, nil)
	alt = findAlternatePath(topo2, "A", "C", map[string]bool{"L1": true, "L2": true})
	if alt != nil {
		t.Fatalf("expected no backup path without L3/L4, got %v", alt)
	}
	// Unidirectional L3 must not be traversable D->A.
	alt = findAlternatePath(topo, "D", "A", map[string]bool{"L4": true, "L2": true, "L1": true})
	if alt != nil {
		t.Fatalf("unidirectional L3 must not allow D->A, got %v", alt)
	}
}

func TestValidateReportContents(t *testing.T) {
	sc := testScenario()
	report := Validate(sc)
	for _, want := range []string{
		"=== Scenario: baseline ===",
		"=== Scenario: failure: cut-L1",
		"OVERLOAD",
		"minimal overload flow set (remove to fix): {F2}",
		"UNREACHABLE flow F1",
		"UNREACHABLE flow F3",
		"backup available: A->D->C",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q\nreport:\n%s", want, report)
		}
	}
}

package main

import (
	"reflect"
	"testing"
)

func netForAnalysis(t *testing.T) *Network {
	t.Helper()
	in := &Input{
		Name: "analysis",
		Links: []Link{
			{ID: "AB", From: "A", To: "B", Capacity: 10, Mode: ModeSeparate},
			{ID: "BC", From: "B", To: "C", Capacity: 4, Mode: ModeShared},
			{ID: "AC", From: "A", To: "C", Capacity: 10, Mode: ModeSeparate},
			{ID: "DE", From: "D", To: "E", Capacity: 5, Mode: ModeSeparate},
		},
		Flows: []Flow{
			{ID: "F1", Src: "A", Dst: "B", Demand: 6, Path: []string{"AB"}},
			{ID: "F2", Src: "A", Dst: "B", Demand: 6, Path: []string{"AB"}},
			{ID: "F3", Src: "B", Dst: "C", Demand: 3, Path: []string{"BC"}},
			{ID: "F4", Src: "C", Dst: "B", Demand: 3, Path: []string{"BC"}},
			{ID: "F5", Src: "D", Dst: "E", Demand: 2, Path: []string{"DE"}},
		},
		FailureScenarios: []FailureScenario{{ID: "S1", FailedLinks: []string{"AB"}}},
	}
	n, err := BuildNetwork(in)
	if err != nil {
		t.Fatalf("BuildNetwork: %v", err)
	}
	return n
}

func verdictOf(rep ScenarioReport, linkID string) *LinkVerdict {
	for i := range rep.Verdicts {
		if rep.Verdicts[i].LinkID == linkID {
			return &rep.Verdicts[i]
		}
	}
	return nil
}

func TestSeparateLinkLoadAccumulatesFlows(t *testing.T) {
	n := netForAnalysis(t)
	rep := analyzeScenario(n, "", nil)
	v := verdictOf(rep, "AB")
	if v == nil {
		t.Fatal("no verdict for AB")
	}
	if got := v.Dirs[DirFwd].Load; got != 12 {
		t.Fatalf("AB fwd load = %v, want 12 (F1+F2)", got)
	}
	if !v.Overloaded {
		t.Fatal("AB should be overloaded (12 > 10)")
	}
	want := [][]string{{"F1", "F2"}}
	if !reflect.DeepEqual(v.MinimalSets, want) {
		t.Fatalf("minimal sets = %v, want %v", v.MinimalSets, want)
	}
}

func TestSharedLinkCombinesBothDirections(t *testing.T) {
	n := netForAnalysis(t)
	rep := analyzeScenario(n, "", nil)
	v := verdictOf(rep, "BC")
	if v == nil {
		t.Fatal("no verdict for BC")
	}
	if got := v.CombinedLoad; got != 6 {
		t.Fatalf("BC combined load = %v, want 6 (F3 fwd + F4 rev)", got)
	}
	if !v.Overloaded {
		t.Fatal("BC should be overloaded in shared mode (6 > 4)")
	}
	want := [][]string{{"F3", "F4"}}
	if !reflect.DeepEqual(v.MinimalSets, want) {
		t.Fatalf("minimal sets = %v, want %v", v.MinimalSets, want)
	}
}

func TestMinimalOverloadSetsEnumeration(t *testing.T) {
	demand := map[string]float64{"a": 6, "b": 5, "c": 4, "d": 1}
	get := func(id string) float64 { return demand[id] }
	sets, trunc := minimalOverloadSets([]string{"a", "b", "c", "d"}, get, 10)
	if trunc {
		t.Fatal("unexpected truncation")
	}
	// Sum is 16 > 10. Inclusion-minimal subsets above 10:
	// {a,b}=11, {a,c}=10 no, {b,c,d}=10 no, {a,c,d}=11, {b,c}=9 no...
	// Exact: {a,b} (11), {a,c,d} (11). {b,c,d}=10 not >10.
	want := [][]string{{"a", "b"}, {"a", "c", "d"}}
	if !reflect.DeepEqual(sets, want) {
		t.Fatalf("minimal sets = %v, want %v", sets, want)
	}
}

func TestGreedyMinimalSetIsMinimal(t *testing.T) {
	ids := []string{}
	demand := map[string]float64{}
	for i := 0; i < 30; i++ {
		id := string(rune('a'+i%26)) + string(rune('0'+i/26))
		ids = append(ids, id)
		demand[id] = float64(i%5 + 1)
	}
	get := func(id string) float64 { return demand[id] }
	sets, trunc := minimalOverloadSets(ids, get, 10)
	if !trunc {
		t.Fatal("expected truncation flag for >20 flows")
	}
	if len(sets) != 1 || !isMinimal(sets[0], get, 10) {
		t.Fatalf("greedy set %v is not minimal", sets)
	}
}

func TestUnreachableFlowNotCountedAsSuccess(t *testing.T) {
	n := netForAnalysis(t)
	rep := analyzeScenario(n, "S1", n.FailedSet(n.Scenarios[0]))
	// F1 and F2 both route over AB and must be reported unreachable.
	want := []string{"F1", "F2"}
	if !reflect.DeepEqual(rep.Unreachable, want) {
		t.Fatalf("unreachable = %v, want %v", rep.Unreachable, want)
	}
	// Their demand must be gone from every remaining link load.
	for _, v := range rep.Verdicts {
		for d := 0; d < 2; d++ {
			for _, id := range v.Dirs[d].FlowIDs {
				if id == "F1" || id == "F2" {
					t.Fatalf("unreachable flow %s still contributes load on %s", id, v.LinkID)
				}
			}
		}
	}
	// And the scenario must not be reported as a clean pass.
	if len(rep.Unreachable) == 0 {
		t.Fatal("unreachable flows silently dropped")
	}
}

func TestRedundancyCheck(t *testing.T) {
	in := &Input{
		Links: []Link{
			{ID: "AB", From: "A", To: "B", Capacity: 10, Mode: ModeSeparate},
			{ID: "AC", From: "A", To: "C", Capacity: 10, Mode: ModeSeparate},
			{ID: "BD", From: "B", To: "D", Capacity: 10, Mode: ModeSeparate},
			{ID: "CD", From: "C", To: "D", Capacity: 10, Mode: ModeSeparate},
			{ID: "DE", From: "D", To: "E", Capacity: 10, Mode: ModeSeparate},
		},
		Flows: []Flow{
			{ID: "F1", Src: "A", Dst: "D", Demand: 5, Path: []string{"AB", "BD"}},
			{ID: "F2", Src: "D", Dst: "E", Demand: 2, Path: []string{"DE"}},
		},
	}
	n, err := BuildNetwork(in)
	if err != nil {
		t.Fatalf("BuildNetwork: %v", err)
	}
	reds := checkRedundancy(n)
	if !reds[0].Redundant {
		t.Fatal("F1 should have link-disjoint backup A->C->D")
	}
	if got := reds[0].AltPath; !reflect.DeepEqual(got, []string{"AC", "CD"}) {
		t.Fatalf("F1 alt path = %v, want [AC CD]", got)
	}
	if reds[1].Redundant {
		t.Fatal("F2 (D->E) has no alternate route and must not be redundant")
	}
}

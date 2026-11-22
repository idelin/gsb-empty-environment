package changeaccept

import (
	"errors"
	"testing"
)

type switchFixture struct {
	input   Input
	groupID [3]GroupID
}

func newSwitchFixture(t *testing.T) switchFixture {
	t.Helper()
	topology := Topology{
		Endpoints: []Endpoint{
			{ID: "s", Tenant: "t1", Egress: allow("old"), Changes: Passthrough()},
			{ID: "m", Tenant: "tx", Ingress: allow("old"), Egress: allow("old"), Changes: Passthrough()},
			{ID: "y", Tenant: "ty", Ingress: allow("old", "new"), Egress: denyAll(), Changes: Passthrough()},
			{ID: "z", Tenant: "tz", Ingress: allow("old", "new"), Egress: denyAll(), Changes: Passthrough()},
			{ID: "d", Tenant: "t2", Ingress: allow("old", "new"), Changes: Passthrough()},
		},
		Edges: []Edge{
			{From: "s", To: "m"},
			{From: "m", To: "d"},
			{From: "s", To: "y"},
			{From: "y", To: "d"},
			{From: "s", To: "z"},
			{From: "z", To: "d"},
		},
	}
	auth := mustAuth(t, []TenantID{"t1", "tx", "ty", "tz", "t2"}, map[TenantPair]PairPolicy{
		pair("t1", "t2"): {
			{Source: Labels("old"), Destination: Labels("old"), Action: ActionAllow},
			{Source: Labels("new"), Destination: Labels("new"), Action: ActionAllow},
		},
	})
	groups := []SwitchGroup{
		{
			ID: 1,
			Changes: []PolicyChange{
				{Endpoint: "s", Plane: PlaneEgress, Old: allow("old"), New: allow("new")},
				{Endpoint: "m", Plane: PlaneIngress, Old: allow("old"), New: allow("new")},
			},
		},
		{
			ID: 2,
			Changes: []PolicyChange{
				{Endpoint: "y", Plane: PlaneEgress, Old: denyAll(), New: allow("new")},
			},
		},
		{
			ID: 3,
			Changes: []PolicyChange{
				{Endpoint: "z", Plane: PlaneEgress, Old: denyAll(), New: allow("new")},
			},
		},
	}
	return switchFixture{
		input: Input{
			Topology: topology,
			Auth:     auth,
			Groups:   groups,
			Order:    []GroupID{1, 2, 3},
			Pairs:    []TenantPair{pair("t1", "t2")},
			Probes: []Probe{{
				ID: "probe", Pair: pair("t1", "t2"), Source: "s", Destination: "d",
			}},
		},
		groupID: [3]GroupID{1, 2, 3},
	}
}

func TestEveryEventInterleavingAndMinimumReorder(t *testing.T) {
	fixture := newSwitchFixture(t)
	orders := permutations([]GroupID{1, 2, 3})
	for _, order := range orders {
		report, err := fixture.input.CheckOrder(order)
		if err != nil {
			t.Fatalf("CheckOrder(%v): %v", order, err)
		}
		wantCompliant := order[0] != 1
		if (report.Outcome == OutcomeCompliant) != wantCompliant {
			t.Fatalf("order %v: got %s, want compliant=%v; prefixes=%+v", order, report.Outcome, wantCompliant, report.Prefixes)
		}
	}

	report, err := fixture.input.FindMinimalOrder()
	if err != nil {
		t.Fatalf("FindMinimalOrder: %v", err)
	}
	if report.Outcome != OutcomeCompliant {
		t.Fatalf("reorder outcome = %s", report.Outcome)
	}
	want := []GroupID{2, 1, 3}
	if len(report.Order) != len(want) {
		t.Fatalf("reorder = %v", report.Order)
	}
	for index := range want {
		if report.Order[index] != want[index] {
			t.Fatalf("reorder = %v, want %v (minimum swaps with ID tie-break)", report.Order, want)
		}
	}
	if report.Swaps != 1 {
		t.Fatalf("swaps = %d, want 1", report.Swaps)
	}
	distance, err := AdjacentSwapDistance([]GroupID{1, 2, 3}, report.Order)
	if err != nil || distance != report.Swaps {
		t.Fatalf("distance = %d, %v; report swaps = %d", distance, err, report.Swaps)
	}
	for index, prefix := range report.Prefixes {
		for position, id := range prefix.Applied {
			if id != report.Order[position] {
				t.Fatalf("prefix %d applied order = %v", index, prefix.Applied)
			}
		}
	}
}

func TestAcceptKeepsOriginalVerdictAndReturnsRecommendedOrder(t *testing.T) {
	fixture := newSwitchFixture(t)
	result, err := fixture.input.Accept()
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if result.Given.Outcome != OutcomeNonCompliant {
		t.Fatalf("given outcome = %s", result.Given.Outcome)
	}
	if result.Recommended == nil || result.Recommended.Outcome != OutcomeCompliant {
		t.Fatalf("recommended = %+v", result.Recommended)
	}
	if result.Outcome != OutcomeCompliant {
		t.Fatalf("final outcome = %s", result.Outcome)
	}
}

func TestDependencyConstrainedReorder(t *testing.T) {
	fixture := newSwitchFixture(t)
	// Make both bypass groups depend on group 1. No compliant order then exists:
	// group 1 must be first, which destroys liveness before a bypass is enabled.
	fixture.input.Groups[1].DependsOn = []GroupID{1}
	fixture.input.Groups[2].DependsOn = []GroupID{1}
	report, err := fixture.input.FindMinimalOrder()
	if err != nil {
		t.Fatalf("FindMinimalOrder: %v", err)
	}
	if report.Outcome != OutcomeNonCompliant {
		t.Fatalf("dependency-constrained outcome = %s, want non-compliant", report.Outcome)
	}
}

func TestCandidateOrderMustRespectDependencies(t *testing.T) {
	fixture := newSwitchFixture(t)
	fixture.input.Groups[1].DependsOn = []GroupID{1}
	_, err := fixture.input.CheckOrder([]GroupID{2, 1, 3})
	var dependencyError *OrderDependencyError
	if !errors.As(err, &dependencyError) {
		t.Fatalf("CheckOrder error = %v, want OrderDependencyError", err)
	}
}

func TestReorderBudgetExhaustionIsUndetermined(t *testing.T) {
	fixture := newSwitchFixture(t)
	fixture.input.Budget = Budget{MaxSubsets: 1}
	report, err := fixture.input.FindMinimalOrder()
	if err != nil {
		t.Fatalf("FindMinimalOrder: %v", err)
	}
	if report.Outcome != OutcomeUndetermined {
		t.Fatalf("reorder budget outcome = %s, want undetermined", report.Outcome)
	}
}

func permutations(values []GroupID) [][]GroupID {
	var result [][]GroupID
	var visit func(int)
	visit = func(index int) {
		if index == len(values) {
			result = append(result, append([]GroupID(nil), values...))
			return
		}
		for next := index; next < len(values); next++ {
			values[index], values[next] = values[next], values[index]
			visit(index + 1)
			values[index], values[next] = values[next], values[index]
		}
	}
	visit(0)
	return result
}

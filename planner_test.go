package iac

import (
	"errors"
	"reflect"
	"testing"
)

func mustPlan(t *testing.T, resources []Resource, opts ...PlanOptions) *Plan {
	t.Helper()
	plan, err := BuildPlan(resources, opts...)
	if err != nil {
		t.Fatalf("BuildPlan failed: %v", err)
	}
	return plan
}

func actionOf(t *testing.T, plan *Plan, address string) ActionKind {
	t.Helper()
	a, ok := plan.Action(address)
	if !ok {
		t.Fatalf("plan has no action for %q", address)
	}
	return a
}

func waveOf(t *testing.T, plan *Plan, nodeID string) int {
	t.Helper()
	for _, w := range plan.Waves {
		for _, id := range w.NodeIDs {
			if id == nodeID {
				return w.Index
			}
		}
	}
	t.Fatalf("node %q is not in any wave", nodeID)
	return 0
}

func planAddresses(plan *Plan) []string {
	out := make([]string, 0, len(plan.Resources))
	for _, r := range plan.Resources {
		out = append(out, r.Address)
	}
	return out
}

func TestCBDPropagatesUpstreamAcrossMultipleLevels(t *testing.T) {
	resources := []Resource{
		{Address: "net", Change: ProposedChange{Action: Replace}},
		{Address: "db", DependsOn: []string{"net"}, Change: ProposedChange{Action: Replace}},
		{
			Address:   "app",
			DependsOn: []string{"db"},
			Change:    ProposedChange{Action: Replace},
			Lifecycle: Lifecycle{CreateBeforeDestroy: true},
		},
	}

	plan := mustPlan(t, resources)
	for _, addr := range []string{"app", "db", "net"} {
		if got := actionOf(t, plan, addr); got != CreateThenDestroy {
			t.Fatalf("%s action = %s, want %s", addr, got, CreateThenDestroy)
		}
	}

	for _, edge := range [][2]string{
		{"create:net", "create:db"},
		{"create:db", "create:app"},
		{"create:app", "destroy:app"},
		{"destroy:app", "destroy:db"},
		{"destroy:db", "destroy:net"},
		// Old db must survive until the new app exists.
		{"create:app", "destroy:db"},
		// Old net must survive until the new db exists.
		{"create:db", "destroy:net"},
	} {
		if !plan.Graph.HasEdge(edge[0], edge[1]) {
			t.Errorf("missing edge %s -> %s", edge[0], edge[1])
		}
	}

	wantWaveOrder := []string{
		"create:net",
		"create:db",
		"create:app",
		"destroy:app",
		"destroy:db",
		"destroy:net",
	}
	for i, node := range wantWaveOrder {
		if got := waveOf(t, plan, node); got != i+1 {
			t.Errorf("wave(%s) = %d, want %d", node, got, i+1)
		}
	}
}

func TestForbidCBDReportsConflictPath(t *testing.T) {
	resources := []Resource{
		{
			Address:   "app",
			DependsOn: []string{"db"},
			Change:    ProposedChange{Action: Replace},
			Lifecycle: Lifecycle{CreateBeforeDestroy: true},
		},
		{Address: "db", DependsOn: []string{"net"}, Change: ProposedChange{Action: Replace}},
		{
			Address:   "net",
			Change:    ProposedChange{Action: Replace},
			Lifecycle: Lifecycle{ForbidCBD: true},
		},
	}

	plan, err := BuildPlan(resources)
	if plan != nil {
		t.Fatalf("expected nil plan on CBD conflict")
	}
	if !HasErrorCode(err, ErrorCBDConflict) {
		t.Fatalf("error = %v, want CBD_CONFLICT", err)
	}
	var pe *PlanError
	if !errors.As(err, &pe) {
		t.Fatalf("error type = %T, want *PlanError", err)
	}
	want := []string{"app", "db", "net"}
	if !reflect.DeepEqual(pe.Chain, want) {
		t.Fatalf("conflict chain = %#v, want %#v", pe.Chain, want)
	}
	if len(pe.Chains) != 1 || !reflect.DeepEqual(pe.Chains[0], want) {
		t.Fatalf("conflict chains = %#v, want [%#v]", pe.Chains, want)
	}
}

func TestReplaceTriggeredByCascadesAndPreventDestroyBlocks(t *testing.T) {
	resources := []Resource{
		{Address: "config", Change: ProposedChange{Action: UpdateInPlace}},
		{
			Address:            "cache",
			ReplaceTriggeredBy: []string{"config"},
			Lifecycle:          Lifecycle{PreventDestroy: true},
		},
	}

	plan, err := BuildPlan(resources)
	if plan != nil {
		t.Fatalf("expected nil plan on prevent_destroy violation")
	}
	if !HasErrorCode(err, ErrorPreventDestroy) {
		t.Fatalf("error = %v, want PREVENT_DESTROY", err)
	}
	var pe *PlanError
	if !errors.As(err, &pe) {
		t.Fatalf("error type = %T, want *PlanError", err)
	}
	if len(pe.Violations) != 1 {
		t.Fatalf("violations = %#v, want 1", pe.Violations)
	}
	v := pe.Violations[0]
	if v.Address != "cache" || v.Action != DestroyThenCreate {
		t.Fatalf("violation = %#v, want cache DestroyThenCreate", v)
	}
	if want := []string{"config", "cache"}; !reflect.DeepEqual(v.Chain, want) {
		t.Fatalf("trigger chain = %#v, want %#v", v.Chain, want)
	}
}

func TestReplaceTriggeredByCascadesTransitively(t *testing.T) {
	resources := []Resource{
		{Address: "a", Change: ProposedChange{Action: UpdateInPlace}},
		{Address: "b", ReplaceTriggeredBy: []string{"a"}},
		{Address: "c", ReplaceTriggeredBy: []string{"b"}},
	}
	plan := mustPlan(t, resources)
	if got := actionOf(t, plan, "b"); got != DestroyThenCreate {
		t.Fatalf("b action = %s, want %s", got, DestroyThenCreate)
	}
	if got := actionOf(t, plan, "c"); got != DestroyThenCreate {
		t.Fatalf("c action = %s, want %s", got, DestroyThenCreate)
	}
	for _, r := range plan.Resources {
		if r.Address == "c" {
			if want := []string{"a", "b", "c"}; !reflect.DeepEqual(r.TriggerChain, want) {
				t.Fatalf("c trigger chain = %#v, want %#v", r.TriggerChain, want)
			}
		}
	}
}

func TestApplyAndDestroyTargetClosuresUseOppositeDirections(t *testing.T) {
	resources := []Resource{
		{Address: "root"},
		{Address: "middle", DependsOn: []string{"root"}},
		{Address: "leaf", DependsOn: []string{"middle"}},
		{Address: "orphan", DependsOn: []string{"leaf"}},
		{Address: "unrelated"},
	}

	apply := mustPlan(t, resources, PlanOptions{Targets: []string{"leaf"}})
	if want := []string{"leaf", "middle", "root"}; !reflect.DeepEqual(planAddresses(apply), want) {
		t.Fatalf("apply target closure = %#v, want %#v", planAddresses(apply), want)
	}

	destroyPlan := mustPlan(t, resources, PlanOptions{Targets: []string{"leaf"}, Destroy: true})
	if want := []string{"leaf", "orphan"}; !reflect.DeepEqual(planAddresses(destroyPlan), want) {
		t.Fatalf("destroy target closure = %#v, want %#v", planAddresses(destroyPlan), want)
	}
	for _, addr := range []string{"leaf", "orphan"} {
		if got := actionOf(t, destroyPlan, addr); got != Destroy {
			t.Fatalf("%s action = %s, want Destroy", addr, got)
		}
	}
	if !destroyPlan.Graph.HasEdge("destroy:orphan", "destroy:leaf") {
		t.Fatalf("downstream orphan must be destroyed before its target")
	}

	up, err := UpstreamClosure(resources, "leaf")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"leaf", "middle", "root"}; !reflect.DeepEqual(up, want) {
		t.Fatalf("UpstreamClosure = %#v, want %#v", up, want)
	}
	down, err := DownstreamClosure(resources, "leaf")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"leaf", "orphan"}; !reflect.DeepEqual(down, want) {
		t.Fatalf("DownstreamClosure = %#v, want %#v", down, want)
	}
}

func TestReplacementNodesAreSplitAndAcyclic(t *testing.T) {
	resources := []Resource{
		{Address: "b", Change: ProposedChange{Action: Replace}},
		{Address: "a", DependsOn: []string{"b"}, Change: ProposedChange{Action: Replace}},
	}
	plan := mustPlan(t, resources)
	if got, want := len(plan.Graph.Nodes), 4; got != want {
		t.Fatalf("node count = %d, want %d", got, want)
	}
	if cycle := plan.Graph.FindMinimalCycle(); cycle != nil {
		t.Fatalf("unexpected cycle: %v", cycle)
	}
	for _, edge := range [][2]string{
		{"destroy:b", "create:b"},
		{"destroy:a", "create:a"},
		{"create:b", "create:a"},
		{"destroy:a", "destroy:b"},
	} {
		if !plan.Graph.HasEdge(edge[0], edge[1]) {
			t.Errorf("missing edge %s -> %s", edge[0], edge[1])
		}
	}
	for node, wantWave := range map[string]int{
		"destroy:a": 1,
		"destroy:b": 2,
		"create:b":  3,
		"create:a":  4,
	} {
		if got := waveOf(t, plan, node); got != wantWave {
			t.Errorf("wave(%s) = %d, want %d", node, got, wantWave)
		}
	}
}

func TestMinimalExecutionCycleIsReported(t *testing.T) {
	g := NewGraph()
	for _, id := range []string{"a", "b", "c", "d"} {
		if err := g.AddNode(&Node{ID: id, Address: id}); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range [][2]string{{"a", "b"}, {"b", "c"}, {"c", "a"}, {"c", "d"}} {
		if err := g.AddEdge(e[0], e[1]); err != nil {
			t.Fatal(err)
		}
	}
	cycle := g.FindMinimalCycle()
	if want := []string{"a", "b", "c", "a"}; !reflect.DeepEqual(cycle, want) {
		t.Fatalf("minimal cycle = %#v, want %#v", cycle, want)
	}
	if err := g.Validate(); !HasErrorCode(err, ErrorExecutionCycle) {
		t.Fatalf("Validate error = %v, want EXECUTION_CYCLE", err)
	}
	if _, err := g.TopologicalWaves(); !HasErrorCode(err, ErrorExecutionCycle) {
		t.Fatalf("TopologicalWaves error = %v, want EXECUTION_CYCLE", err)
	}
}

func TestDependencyCycleIsReported(t *testing.T) {
	resources := []Resource{
		{Address: "a", DependsOn: []string{"b"}},
		{Address: "b", DependsOn: []string{"a"}},
		{Address: "c"},
	}
	plan, err := BuildPlan(resources)
	if plan != nil {
		t.Fatalf("expected nil plan on dependency cycle")
	}
	if !HasErrorCode(err, ErrorDependencyCycle) {
		t.Fatalf("error = %v, want DEPENDENCY_CYCLE", err)
	}
	var pe *PlanError
	if !errors.As(err, &pe) {
		t.Fatalf("error type = %T, want *PlanError", err)
	}
	if want := []string{"a", "b", "a"}; !reflect.DeepEqual(pe.Cycle, want) {
		t.Fatalf("cycle = %#v, want %#v", pe.Cycle, want)
	}
}

func TestDriftDetectionChoosesUpdateCreateOrReplace(t *testing.T) {
	resources := []Resource{
		{
			Address: "missing",
			Remote:  &RemoteState{Exists: false},
			Desired: map[string]any{"size": "1"},
		},
		{
			Address:            "update",
			Remote:             &RemoteState{Exists: true, Attributes: map[string]any{"size": "1", "tags": map[string]any{"env": "dev"}}},
			Desired:            map[string]any{"size": "1", "tags": map[string]any{"env": "prod"}},
			ForceNewAttributes: []string{"size"},
		},
		{
			Address:            "replace",
			Remote:             &RemoteState{Exists: true, Attributes: map[string]any{"size": "1"}},
			Desired:            map[string]any{"size": "2"},
			ForceNewAttributes: []string{"size"},
		},
		{
			Address: "steady",
			Remote:  &RemoteState{Exists: true, Attributes: map[string]any{"size": "1"}},
			Desired: map[string]any{"size": "1"},
		},
	}
	plan := mustPlan(t, resources)
	if got := actionOf(t, plan, "missing"); got != Create {
		t.Errorf("missing action = %s, want Create", got)
	}
	if got := actionOf(t, plan, "update"); got != UpdateInPlace {
		t.Errorf("update action = %s, want UpdateInPlace", got)
	}
	if got := actionOf(t, plan, "replace"); got != DestroyThenCreate {
		t.Errorf("replace action = %s, want DestroyThenCreate", got)
	}
	if got := actionOf(t, plan, "steady"); got != NoOp {
		t.Errorf("steady action = %s, want NoOp", got)
	}
}

func TestPreventDestroyBlocksDirectDestroyAndReplacement(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change ProposedChange
	}{
		{name: "direct", change: ProposedChange{Action: Destroy}},
		{name: "force-new", change: ProposedChange{Action: UpdateInPlace, ForceNew: true}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := BuildPlan([]Resource{{
				Address:   "prod-db",
				Change:    tt.change,
				Lifecycle: Lifecycle{PreventDestroy: true},
			}})
			if !HasErrorCode(err, ErrorPreventDestroy) {
				t.Fatalf("error = %v, want PREVENT_DESTROY", err)
			}
		})
	}
}

func TestForceNewTriggeredReplaceListsFullChain(t *testing.T) {
	resources := []Resource{
		{Address: "source", Change: ProposedChange{Action: UpdateInPlace, ForceNew: true}},
		{
			Address:            "dependent",
			ReplaceTriggeredBy: []string{"source"},
			Lifecycle:          Lifecycle{PreventDestroy: true},
		},
	}
	_, err := BuildPlan(resources)
	if !HasErrorCode(err, ErrorPreventDestroy) {
		t.Fatalf("error = %v, want PREVENT_DESTROY", err)
	}
	var pe *PlanError
	if !errors.As(err, &pe) {
		t.Fatalf("error type = %T, want *PlanError", err)
	}
	if len(pe.Violations) != 1 {
		t.Fatalf("violations = %#v, want 1", pe.Violations)
	}
	if want := []string{"source", "dependent"}; !reflect.DeepEqual(pe.Violations[0].Chain, want) {
		t.Fatalf("chain = %#v, want %#v", pe.Violations[0].Chain, want)
	}
}

func TestDestroyTargetHonorsPreventDestroy(t *testing.T) {
	resources := []Resource{
		{Address: "root"},
		{Address: "child", DependsOn: []string{"root"}, Lifecycle: Lifecycle{PreventDestroy: true}},
	}
	_, err := BuildPlan(resources, PlanOptions{Targets: []string{"root"}, Destroy: true})
	if !HasErrorCode(err, ErrorPreventDestroy) {
		t.Fatalf("error = %v, want PREVENT_DESTROY", err)
	}
}

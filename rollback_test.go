package main

import (
	"encoding/json"
	"io/ioutil"
	"testing"
)

func mustGraph(t *testing.T, sc *Scenario) *Graph {
	t.Helper()
	g, err := BuildGraph(sc)
	if err != nil {
		t.Fatalf("BuildGraph: %v", err)
	}
	return g
}

func exampleGraph(t *testing.T) *Graph {
	t.Helper()
	var sc Scenario
	if err := json.Unmarshal([]byte(exampleJSON), &sc); err != nil {
		t.Fatalf("parse example: %v", err)
	}
	return mustGraph(t, &sc)
}

func TestExampleJSONInSync(t *testing.T) {
	data, err := ioutil.ReadFile("example.json")
	if err != nil {
		t.Fatalf("read example.json: %v", err)
	}
	if string(data) != exampleJSON {
		t.Fatalf("example.json and the embedded exampleJSON constant differ")
	}
}

func TestSelectCommonTarget(t *testing.T) {
	g := exampleGraph(t)
	plan, err := SelectCommonTarget(g)
	if err != nil {
		t.Fatalf("SelectCommonTarget: %v", err)
	}
	if plan.Target != "v3" {
		t.Fatalf("target = %q, want v3", plan.Target)
	}
	if len(plan.RetainedFeatures) != 3 {
		t.Fatalf("retained %d features, want 3", len(plan.RetainedFeatures))
	}
	if plan.TotalSteps != 2 {
		t.Fatalf("total steps = %d, want 2", plan.TotalSteps)
	}
	if err := VerifyPlan(g, plan); err != nil {
		t.Fatalf("VerifyPlan: %v", err)
	}
}

func TestSelectCommonTargetPrefersFeaturesOverSteps(t *testing.T) {
	// Common candidates are v2 (2 features, 1 total step) and v1
	// (1 feature, 3 total steps). More retained features must win even
	// though v1 is not cheaper in steps here; the key check is that the
	// feature count dominates the step count.
	sc := &Scenario{
		Versions: []Version{
			{ID: "v1", Features: []string{"core"}},
			{ID: "v2", Features: []string{"core", "f2"}},
			{ID: "v3", Features: []string{"core", "f2", "f3"}},
		},
		Edges: []Edge{
			{From: "v2", To: "v1", Cost: 1, Undoes: []string{"f2"}},
			{From: "v3", To: "v2", Cost: 1, Undoes: []string{"f3"}},
		},
		Nodes: []Node{
			{ID: "A", Version: "v2"},
			{ID: "B", Version: "v3"},
		},
		Budget: 10,
	}
	g := mustGraph(t, sc)
	plan, err := SelectCommonTarget(g)
	if err != nil {
		t.Fatalf("SelectCommonTarget: %v", err)
	}
	if plan.Target != "v2" {
		t.Fatalf("target = %q, want v2 (more retained features than v1)", plan.Target)
	}
	if plan.TotalSteps != 1 {
		t.Fatalf("total steps = %d, want 1 (B: v3 -> v2)", plan.TotalSteps)
	}
}

func TestSelectCommonTargetMinStepsTieBreak(t *testing.T) {
	// Common candidates v3 and v2 retain the same number of features;
	// the one reachable with fewer total steps (v3) must win.
	sc := &Scenario{
		Versions: []Version{
			{ID: "v2", Features: []string{"core", "f2"}},
			{ID: "v3", Features: []string{"core", "f2"}},
			{ID: "v4", Features: []string{"core", "f2", "f4"}},
		},
		Edges: []Edge{
			{From: "v4", To: "v2", Cost: 1, Undoes: []string{"f4"}},
			{From: "v4", To: "v3", Cost: 1, Undoes: []string{"f4"}},
			{From: "v3", To: "v2", Cost: 1},
		},
		Nodes: []Node{
			{ID: "A", Version: "v4"},
			{ID: "B", Version: "v3"},
		},
		Budget: 10,
	}
	g := mustGraph(t, sc)
	plan, err := SelectCommonTarget(g)
	if err != nil {
		t.Fatalf("SelectCommonTarget: %v", err)
	}
	if plan.Target != "v3" {
		t.Fatalf("target = %q, want v3 (same features as v2, fewer steps)", plan.Target)
	}
	if plan.TotalSteps != 1 {
		t.Fatalf("total steps = %d, want 1", plan.TotalSteps)
	}
	if err := VerifyPlan(g, plan); err != nil {
		t.Fatalf("VerifyPlan: %v", err)
	}
}

func TestSelectCommonTargetNoCommon(t *testing.T) {
	sc := &Scenario{
		Versions: []Version{
			{ID: "a1", Features: []string{"x"}},
			{ID: "b1", Features: []string{"y"}},
		},
		Nodes: []Node{
			{ID: "A", Version: "a1"},
			{ID: "B", Version: "b1"},
		},
		Budget: 10,
	}
	g := mustGraph(t, sc)
	if _, err := SelectCommonTarget(g); err == nil {
		t.Fatalf("expected error for disconnected version graphs")
	}
}

func TestVerifyPlanRejectsTampering(t *testing.T) {
	g := exampleGraph(t)
	plan, err := SelectCommonTarget(g)
	if err != nil {
		t.Fatalf("SelectCommonTarget: %v", err)
	}
	// Tamper: path uses a non-existent edge.
	bad := *plan
	bad.NodePlans = append([]NodePlan(nil), plan.NodePlans...)
	bad.NodePlans[0].Path = []string{"v5", "v1"}
	if err := VerifyPlan(g, &bad); err == nil {
		t.Fatalf("expected verification to reject a path with a non-existent edge")
	}
	// Tamper: wrong target.
	bad2 := *plan
	bad2.Target = "v2"
	if err := VerifyPlan(g, &bad2); err == nil {
		t.Fatalf("expected verification to reject a wrong target")
	}
	// Tamper: suboptimal target claim (v1 retains fewer features).
	bad3 := *plan
	bad3.Target = "v1"
	bad3.RetainedFeatures = g.sortedFeatures("v1")
	bad3.NodePlans = append([]NodePlan(nil), plan.NodePlans...)
	for i := range bad3.NodePlans {
		bad3.NodePlans[i].Path = []string{bad3.NodePlans[i].Path[0], "v1"}
	}
	if err := VerifyPlan(g, &bad3); err == nil {
		t.Fatalf("expected verification to reject a suboptimal/invalid plan")
	}
}

func TestBuildGraphRejectsCycle(t *testing.T) {
	sc := &Scenario{
		Versions: []Version{
			{ID: "v1", Features: []string{"a"}},
			{ID: "v2", Features: []string{"a"}},
		},
		Edges: []Edge{
			{From: "v1", To: "v2", Cost: 1},
			{From: "v2", To: "v1", Cost: 1},
		},
		Nodes:  []Node{{ID: "A", Version: "v1"}},
		Budget: 10,
	}
	if _, err := BuildGraph(sc); err == nil {
		t.Fatalf("expected cycle detection to reject the scenario")
	}
}

func TestBuildGraphRejectsTooManyNodes(t *testing.T) {
	sc := &Scenario{
		Versions: []Version{{ID: "v1", Features: []string{"a"}}},
		Budget:   10,
	}
	for _, id := range []string{"n1", "n2", "n3", "n4", "n5", "n6"} {
		sc.Nodes = append(sc.Nodes, Node{ID: id, Version: "v1"})
	}
	if _, err := BuildGraph(sc); err == nil {
		t.Fatalf("expected rejection of more than 5 nodes")
	}
}

package main

import (
	"strings"
	"testing"
)

func TestSearchTrajectoryExample(t *testing.T) {
	g := exampleGraph(t)
	plan, err := SelectCommonTarget(g)
	if err != nil {
		t.Fatalf("SelectCommonTarget: %v", err)
	}
	traj, err := SearchTrajectory(g, plan.Target)
	if err != nil {
		t.Fatalf("SearchTrajectory: %v", err)
	}
	if err := VerifyTrajectory(g, traj, plan.Target); err != nil {
		t.Fatalf("VerifyTrajectory: %v", err)
	}
	if traj.TotalCost > g.scenario.Budget {
		t.Fatalf("trajectory cost %d exceeds budget %d", traj.TotalCost, g.scenario.Budget)
	}
	// The compatibility rule forbids (A=v4, B=v3); make sure no
	// intermediate state contains it.
	for k, st := range traj.States {
		if st[0] == "v4" && st[1] == "v3" {
			t.Fatalf("state %d %v violates the A/B compatibility rule", k, st)
		}
	}
	// Final state: every node at the target.
	for i, v := range traj.Goal {
		last := traj.States[len(traj.States)-1]
		if last[i] != v || v != plan.Target {
			t.Fatalf("final state %v does not match goal %v", last, traj.Goal)
		}
	}
}

func TestSearchRespectsBudget(t *testing.T) {
	g := exampleGraph(t)
	// Cheapest trajectories to v3: 2 moves at cost 5 (A jumps v5->v3)
	// or 3 moves at cost 4 (A walks v5->v4->v3). A budget of 3 makes
	// every trajectory infeasible.
	g.scenario.Budget = 3
	if _, err := SearchTrajectory(g, "v3"); err == nil ||
		!strings.Contains(err.Error(), "no legal trajectory") {
		t.Fatalf("expected infeasible-budget error, got %v", err)
	}
	g.scenario.Budget = 4
	traj, err := SearchTrajectory(g, "v3")
	if err != nil {
		t.Fatalf("SearchTrajectory with exact budget: %v", err)
	}
	if traj.TotalCost != 4 {
		t.Fatalf("total cost = %d, want 4", traj.TotalCost)
	}
	if err := VerifyTrajectory(g, traj, "v3"); err != nil {
		t.Fatalf("VerifyTrajectory: %v", err)
	}
}

func TestSearchRespectsForbidden(t *testing.T) {
	// Node B has an irreversible operation forbidding v2; a plan that
	// would route B through v2 must be rejected by the search.
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
			{ID: "A", Version: "v1"},
			{ID: "B", Version: "v3", Forbidden: []string{"v2"}},
		},
		Budget: 10,
	}
	g := mustGraph(t, sc)
	// Target v1 requires B to pass v2, which is forbidden.
	if _, err := SearchTrajectory(g, "v1"); err == nil {
		t.Fatalf("expected search to fail: B cannot pass forbidden v2")
	}
	// Target v3 is trivially reachable (B stays, A is already... A is
	// at v1 which cannot roll forward, so v3 is impossible for A).
	if _, err := SearchTrajectory(g, "v3"); err == nil {
		t.Fatalf("expected search to fail: A cannot roll forward to v3")
	}
}

func TestSearchRespectsCompatibility(t *testing.T) {
	// A and B must stay on the same version at all times; only a
	// lock-step trajectory is legal.
	sc := &Scenario{
		Versions: []Version{
			{ID: "v1", Features: []string{"core"}},
			{ID: "v2", Features: []string{"core", "f2"}},
		},
		Edges: []Edge{
			{From: "v2", To: "v1", Cost: 1, Undoes: []string{"f2"}},
		},
		Nodes: []Node{
			{ID: "A", Version: "v2"},
			{ID: "B", Version: "v2"},
		},
		Budget: 10,
		Compat: []CompatRule{
			{
				NodeA: "A",
				NodeB: "B",
				AllowedPairs: [][2]string{
					{"v2", "v2"},
					{"v1", "v1"},
				},
			},
		},
	}
	g := mustGraph(t, sc)
	// Lock-step is impossible with single-node moves: any move breaks
	// compatibility, so no trajectory to v1 may exist.
	if _, err := SearchTrajectory(g, "v1"); err == nil {
		t.Fatalf("expected no legal trajectory under lock-step compatibility")
	}
	traj, err := SearchTrajectory(g, "v2")
	if err != nil {
		t.Fatalf("SearchTrajectory to current version: %v", err)
	}
	if len(traj.Moves) != 0 {
		t.Fatalf("expected empty trajectory, got %d moves", len(traj.Moves))
	}
	if err := VerifyTrajectory(g, traj, "v2"); err != nil {
		t.Fatalf("VerifyTrajectory: %v", err)
	}
}

func TestSearchFindsCheapestOrder(t *testing.T) {
	// Without constraints A could jump v5->v3 directly (cost 4). The
	// compatibility rule forbids (A=v5, B=v3) and (A=v4, B=v3), so A
	// must finish before B moves, forcing the v5->v4->v3 route.
	sc := &Scenario{
		Versions: []Version{
			{ID: "v3", Features: []string{"core"}},
			{ID: "v4", Features: []string{"core", "f4"}},
			{ID: "v5", Features: []string{"core", "f4", "f5"}},
		},
		Edges: []Edge{
			{From: "v4", To: "v3", Cost: 1, Undoes: []string{"f4"}},
			{From: "v5", To: "v4", Cost: 2, Undoes: []string{"f5"}},
			{From: "v5", To: "v3", Cost: 4, Undoes: []string{"f4", "f5"}},
		},
		Nodes: []Node{
			{ID: "A", Version: "v5"},
			{ID: "B", Version: "v4"},
		},
		Budget: 10,
		Compat: []CompatRule{
			{
				NodeA: "A",
				NodeB: "B",
				AllowedPairs: [][2]string{
					{"v5", "v4"},
					{"v4", "v4"},
					{"v3", "v4"},
					{"v3", "v3"},
				},
			},
		},
	}
	g := mustGraph(t, sc)
	traj, err := SearchTrajectory(g, "v3")
	if err != nil {
		t.Fatalf("SearchTrajectory: %v", err)
	}
	if err := VerifyTrajectory(g, traj, "v3"); err != nil {
		t.Fatalf("VerifyTrajectory: %v", err)
	}
	// Fewest transitions: A v5->v3 (1) + B v4->v3 (1) = 2 moves.
	if len(traj.Moves) != 2 {
		t.Fatalf("got %d moves, want 2", len(traj.Moves))
	}
	// A must move first: (A=v5,B=v3) is not allowed, so B cannot move
	// while A is still at v5.
	if traj.Moves[0].NodeID != "A" {
		t.Fatalf("first move by %q, want A (compatibility forces order)", traj.Moves[0].NodeID)
	}
}

func TestVerifyTrajectoryRejectsTampering(t *testing.T) {
	g := exampleGraph(t)
	traj, err := SearchTrajectory(g, "v3")
	if err != nil {
		t.Fatalf("SearchTrajectory: %v", err)
	}
	// Tamper: skip an edge.
	bad := *traj
	bad.Moves = append([]Transition(nil), traj.Moves...)
	bad.Moves[0].To = "v1"
	if err := VerifyTrajectory(g, &bad, "v3"); err == nil {
		t.Fatalf("expected verification to reject a tampered move")
	}
	// Tamper: wrong target.
	if err := VerifyTrajectory(g, traj, "v2"); err == nil {
		t.Fatalf("expected verification to reject a wrong target")
	}
}

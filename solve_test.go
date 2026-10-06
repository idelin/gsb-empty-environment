package main

import "testing"

func TestRankCandidates(t *testing.T) {
	p := demoProblem()
	cands := RankCandidates(p)
	want := []Candidate{
		{Target: "v3", Features: 3, TotalSteps: 2},
		{Target: "v2", Features: 2, TotalSteps: 4},
		{Target: "v1", Features: 1, TotalSteps: 5},
		{Target: "v0", Features: 0, TotalSteps: 7},
	}
	if len(cands) != len(want) {
		t.Fatalf("got %d candidates, want %d", len(cands), len(want))
	}
	for i, w := range want {
		if cands[i].Target != w.Target || cands[i].Features != w.Features || cands[i].TotalSteps != w.TotalSteps {
			t.Errorf("rank %d: got %+v, want %+v", i, cands[i], w)
		}
	}
	if err := VerifyCandidate(p, &cands[0]); err != nil {
		t.Errorf("top candidate failed verification: %v", err)
	}
}

func TestSolvePicksV3(t *testing.T) {
	p := demoProblem()
	cand, plan, err := Solve(p)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if cand.Target != "v3" {
		t.Errorf("target = %s, want v3", cand.Target)
	}
	if plan.TotalCost != 5 {
		t.Errorf("total cost = %d, want 5", plan.TotalCost)
	}
	if len(plan.Transitions) != 3 {
		t.Fatalf("got %d transitions, want 3", len(plan.Transitions))
	}
	// N1 must finish (v5 -> v4 -> v3) before N2 moves (v4 -> v3).
	if plan.Transitions[0].Node != 0 || plan.Transitions[1].Node != 0 || plan.Transitions[2].Node != 1 {
		t.Errorf("node execution order violated: %+v", plan.Transitions)
	}
	if err := VerifyPlan(p, plan); err != nil {
		t.Errorf("plan failed verification: %v", err)
	}
}

func TestBudgetTooLowInfeasible(t *testing.T) {
	p := demoProblem()
	p.Budget = 4
	if _, _, err := Solve(p); err == nil {
		t.Error("expected infeasibility with budget 4")
	}
}

func TestForbiddenForcesFallback(t *testing.T) {
	p := demoProblem()
	p.Budget = 20
	p.Nodes[1].Forbidden = feats("v0", "v3") // N2 may never pass through v3
	cand, plan, err := Solve(p)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if cand.Target != "v2" {
		t.Errorf("target = %s, want fallback to v2", cand.Target)
	}
	if err := VerifyPlan(p, plan); err != nil {
		t.Errorf("plan failed verification: %v", err)
	}
}

func TestInitialCompatibilityViolation(t *testing.T) {
	p := demoProblem()
	p.Compatible = func(a, b string) bool { return a == b } // far too strict
	if _, _, err := Solve(p); err == nil {
		t.Error("expected failure: initial state violates compatibility")
	}
}

func TestVerifyPlanRejectsTampering(t *testing.T) {
	p := demoProblem()
	_, plan, err := Solve(p)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	// Swap the first two transitions to break node order.
	bad := *plan
	bad.Transitions = append([]Transition(nil), plan.Transitions...)
	bad.Transitions[0], bad.Transitions[1] = bad.Transitions[1], bad.Transitions[0]
	if err := VerifyPlan(p, &bad); err == nil {
		t.Error("expected verification to reject reordered transitions")
	}
	// Under-report the cost of one edge.
	bad2 := *plan
	bad2.Transitions = append([]Transition(nil), plan.Transitions...)
	bad2.Transitions[0].Edge.Cost = 0
	if err := VerifyPlan(p, &bad2); err == nil {
		t.Error("expected verification to reject falsified edge cost")
	}
}

func TestVerifyCandidateRejectsBrokenPath(t *testing.T) {
	p := demoProblem()
	cand, _, err := Solve(p)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	bad := *cand
	bad.Paths = append([][]string(nil), cand.Paths...)
	bad.Paths[0] = []string{"v5", "v2", "v3"} // v5 -> v2 edge does not exist
	if err := VerifyCandidate(p, &bad); err == nil {
		t.Error("expected verification to reject nonexistent edge")
	}
	bad2 := *cand
	bad2.Features = 99
	if err := VerifyCandidate(p, &bad2); err == nil {
		t.Error("expected verification to reject wrong feature count")
	}
}

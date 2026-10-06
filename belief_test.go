package plango

import (
	"strings"
	"testing"
)

// chargeWorlds models an uncertain charger: "charge" on w0 may only warm it
// (w1) or fully finish it (w2); the observation token reveals which happened.
// w3 mimics w1's observation but is a dead end; w4 can only spin forever.
func chargeProblem(initial Belief) NDProblem {
	return NDProblem{
		Worlds: []World{
			{State: Caps(), Obs: "cold"},          // w0
			{State: Caps(0), Obs: "warm"},         // w1
			{State: Caps(1), Obs: "done"},         // w2 (goal: c1)
			{State: Caps(2), Obs: "warm"},         // w3: dead, looks like w1
			{State: Caps(3), Obs: "spin"},         // w4: loops forever
		},
		Actions: []NDAction{
			{Name: "charge", Trans: [][]int{
				{1, 2}, // w0: uncertain outcome, revealed by observation
				{1},    // w1
				{2},    // w2
				{},     // w3: not applicable
				{},     // w4: not applicable
			}},
			{Name: "fire", Trans: [][]int{
				{},     // w0
				{2},    // w1
				{2},    // w2
				{},     // w3
				{},     // w4
			}},
			{Name: "spin", Trans: [][]int{
				{}, {}, {}, {},
				{4}, // w4 loops without progress
			}},
		},
		Goal:    Caps(1),
		Initial: initial,
	}
}

func TestSolveNDPolicyTree(t *testing.T) {
	p := chargeProblem(BWorlds(0, 1))
	sol := SolveND(p)
	if !sol.Solvable {
		t.Fatalf("expected solvable, dead worlds: %v", sol.DeadWorlds)
	}
	pol := sol.Policy
	if pol.Action != "charge" {
		t.Fatalf("root action should be charge, got %q", pol.Action)
	}
	if pol.Worst != 2 {
		t.Fatalf("worst-case should be 2, got %d", pol.Worst)
	}
	warm, ok := pol.Branches["warm"]
	if !ok {
		t.Fatal("missing branch for observation warm")
	}
	if warm.Action != "fire" || warm.Worst != 1 {
		t.Fatalf("warm branch should fire with worst 1, got %q/%d", warm.Action, warm.Worst)
	}
	done, ok := pol.Branches["done"]
	if !ok {
		t.Fatal("missing branch for observation done")
	}
	if done.Action != "" || done.Worst != 0 {
		t.Fatalf("done branch should be a goal leaf, got %q/%d", done.Action, done.Worst)
	}
	if len(sol.DeadWorlds) == 0 {
		t.Fatal("expected dead worlds w3 and w4 to be reported")
	}
}

func TestSolveNDCosts(t *testing.T) {
	p := chargeProblem(BWorlds(0, 1))
	sol := SolveND(p)
	if !sol.Solvable {
		t.Fatal("expected solvable")
	}
	cases := map[Belief]int{
		BWorlds(2):    0, // already at goal
		BWorlds(1):    1, // fire
		BWorlds(0):    2, // charge, maybe fire
		BWorlds(0, 1): 2, // same policy covers both
	}
	for b, want := range cases {
		if got, ok := sol.Cost[b]; !ok || got != want {
			t.Fatalf("cost[%s]: want %d, got %d (known=%v)", b, want, got, ok)
		}
	}
	if c, ok := sol.Cost[BWorlds(4)]; ok && c != infinity {
		t.Fatalf("spinning world should be unsolvable, got cost %d", c)
	}
}

func TestSolveNDUnsolvable(t *testing.T) {
	// w1 and w3 share the observation "warm" but need different handling:
	// no history-dependent action can save both.
	p := chargeProblem(BWorlds(1, 3))
	sol := SolveND(p)
	if sol.Solvable {
		t.Fatalf("expected unsolvable, got policy:\n%s", sol.Policy)
	}
	if sol.Policy != nil {
		t.Fatal("unsolvable instance must not produce a policy")
	}
	found3 := false
	for _, w := range sol.DeadWorlds {
		if w == 3 {
			found3 = true
		}
	}
	if !found3 {
		t.Fatalf("expected w3 among dead worlds, got %v", sol.DeadWorlds)
	}
}

func TestVerifyPolicyCrossCheck(t *testing.T) {
	p := chargeProblem(BWorlds(0, 1))
	sol := SolveND(p)
	if !sol.Solvable {
		t.Fatal("expected solvable")
	}
	if err := VerifyPolicy(p, sol.Policy); err != nil {
		t.Fatalf("full belief-state verification failed: %v", err)
	}
}

func TestVerifyPolicyRejectsTampering(t *testing.T) {
	p := chargeProblem(BWorlds(0, 1))
	sol := SolveND(p)
	if !sol.Solvable {
		t.Fatal("expected solvable")
	}

	// Drop the "done" branch: the uncertain charge outcome w2 is unhandled.
	broken := *sol.Policy
	branches := make(map[string]*Policy, len(broken.Branches))
	for k, v := range broken.Branches {
		branches[k] = v
	}
	delete(branches, "done")
	broken.Branches = branches
	if err := VerifyPolicy(p, &broken); err == nil {
		t.Fatal("expected verification to reject a policy missing an observation branch")
	}

	// Lie about the worst-case bound.
	liar := *sol.Policy
	liar.Worst = 1
	if err := VerifyPolicy(p, &liar); err == nil {
		t.Fatal("expected verification to reject a wrong worst-case bound")
	}
}

func TestSameHistorySameAction(t *testing.T) {
	// w1 and w3 are indistinguishable after "warm"; any valid policy over
	// {w1, w3} would have to pick one action for both, which cannot exist.
	// Over {w0, w1} the policy must still be a deterministic function of the
	// observation history: the tree has exactly one node per history.
	p := chargeProblem(BWorlds(0, 1))
	sol := SolveND(p)
	if !sol.Solvable {
		t.Fatal("expected solvable")
	}
	rendered := sol.Policy.String()
	if !strings.Contains(rendered, `observe "warm"`) || !strings.Contains(rendered, `observe "done"`) {
		t.Fatalf("policy tree should branch on observations, got:\n%s", rendered)
	}
	if strings.Count(rendered, "do \"charge\"") != 1 {
		t.Fatalf("charge should appear exactly once (history {root}), got:\n%s", rendered)
	}
}

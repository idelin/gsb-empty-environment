package belief

import "testing"

// simulateAll exhaustively replays every world of the initial belief state
// through the policy, branching on every non-deterministic outcome, and
// returns the worst-case number of steps. It is an independent,
// per-world cross-check of the belief-state machinery.
func simulateAll(t *testing.T, p Problem, pol *Policy) int {
	t.Helper()
	worst := 0
	var walk func(w int, node *Policy, depth int)
	walk = func(w int, node *Policy, depth int) {
		if p.Worlds[w].Unsafe {
			t.Fatalf("world %s entered unsafe world", p.Worlds[w].Name)
		}
		if p.Worlds[w].Goal {
			if node.Action != "" {
				t.Fatalf("world %s at goal but policy still acts", p.Worlds[w].Name)
			}
			if depth > worst {
				worst = depth
			}
			return
		}
		var act Action
		found := false
		for _, a := range p.Actions {
			if a.Name == node.Action {
				act, found = a, true
			}
		}
		if !found {
			t.Fatalf("world %s: unknown action %q", p.Worlds[w].Name, node.Action)
		}
		for _, n := range act.Next[w] {
			child, ok := node.Branches[p.Worlds[n].Obs]
			if !ok {
				t.Fatalf("world %s: no branch for observation %q", p.Worlds[w].Name, p.Worlds[n].Obs)
			}
			walk(n, child, depth+1)
		}
	}
	for _, w := range normalize(p.Initial) {
		walk(w, pol, 0)
	}
	return worst
}

// Two indistinguishable start worlds: "try" succeeds from one and fails
// (observably) from the other; the failure branch needs a repair step.
func TestContingentPolicy(t *testing.T) {
	p := Problem{
		Worlds: []World{
			{Name: "ok", Obs: "start"},              // 0
			{Name: "flaky", Obs: "start"},           // 1
			{Name: "done", Obs: "done", Goal: true}, // 2
			{Name: "broken", Obs: "oops"},           // 3
		},
		Actions: []Action{
			{Name: "try", Next: map[int][]int{0: {2}, 1: {3}}},
			{Name: "fix", Next: map[int][]int{3: {2}}},
		},
		Initial: []int{0, 1},
	}
	res := Solve(p)
	if res.Policy == nil {
		t.Fatalf("expected a policy, unsolvable beliefs: %v", res.Unsolvable)
	}
	if res.WorstCase != 2 {
		t.Fatalf("worst case = %d, want 2", res.WorstCase)
	}
	if res.Policy.Action != "try" {
		t.Fatalf("root action = %q, want try", res.Policy.Action)
	}
	if leaf := res.Policy.Branches["done"]; leaf == nil || leaf.Action != "" {
		t.Fatalf("done branch should be a goal leaf, got %+v", leaf)
	}
	fix := res.Policy.Branches["oops"]
	if fix == nil || fix.Action != "fix" {
		t.Fatalf("oops branch should run fix, got %+v", fix)
	}
	if leaf := fix.Branches["done"]; leaf == nil || leaf.Action != "" {
		t.Fatalf("fix/done branch should be a goal leaf, got %+v", leaf)
	}
	worst, err := Verify(p, res.Policy)
	if err != nil {
		t.Fatalf("Verify rejected solver policy: %v", err)
	}
	if worst != res.WorstCase {
		t.Fatalf("Verify worst case %d != solver worst case %d", worst, res.WorstCase)
	}
	if got := simulateAll(t, p, res.Policy); got != res.WorstCase {
		t.Fatalf("per-world simulation worst case %d != %d", got, res.WorstCase)
	}
}

// Non-deterministic outcome revealed only after execution: one action can
// land in two different observations from the same world.
func TestNonDeterministicOutcome(t *testing.T) {
	p := Problem{
		Worlds: []World{
			{Name: "s", Obs: "start"},            // 0
			{Name: "g", Obs: "goal", Goal: true}, // 1
			{Name: "sand", Obs: "stuck"},         // 2
		},
		Actions: []Action{
			{Name: "push", Next: map[int][]int{0: {1, 2}, 2: {2}}},
			{Name: "free", Next: map[int][]int{2: {1}}},
		},
		Initial: []int{0},
	}
	res := Solve(p)
	if res.Policy == nil {
		t.Fatalf("expected a policy, unsolvable beliefs: %v", res.Unsolvable)
	}
	if res.WorstCase != 2 {
		t.Fatalf("worst case = %d, want 2", res.WorstCase)
	}
	if res.Policy.Action != "push" || res.Policy.Branches["stuck"].Action != "free" {
		t.Fatalf("unexpected policy shape: %+v", res.Policy)
	}
	if _, err := Verify(p, res.Policy); err != nil {
		t.Fatalf("Verify rejected solver policy: %v", err)
	}
	simulateAll(t, p, res.Policy)
}

// Same observation history forces the same action: two indistinguishable
// worlds where the only action saves one and dooms the other are
// unsolvable. Making the worlds distinguishable must flip the result.
func TestSameHistorySameAction(t *testing.T) {
	mk := func(obs0, obs1 string) Problem {
		return Problem{
			Worlds: []World{
				{Name: "w0", Obs: obs0},                   // 0
				{Name: "w1", Obs: obs1},                   // 1
				{Name: "g", Obs: "goal", Goal: true},      // 2
				{Name: "doom", Obs: "dead", Unsafe: true}, // 3
			},
			Actions: []Action{
				{Name: "act", Next: map[int][]int{0: {2}, 1: {3}}},
			},
			Initial: []int{0, 1},
		}
	}
	// Indistinguishable: any policy must play "act" or nothing for both
	// worlds; "act" dooms w1, waiting never reaches the goal.
	res := Solve(mk("same", "same"))
	if res.Policy != nil {
		t.Fatal("expected unsolvable for indistinguishable worlds")
	}
	if len(res.Unsolvable) == 0 {
		t.Fatal("expected the unsolvable belief states to be reported")
	}
	foundInitial := false
	for _, b := range res.Unsolvable {
		if len(b) == 2 && b[0] == 0 && b[1] == 1 {
			foundInitial = true
		}
	}
	if !foundInitial {
		t.Fatalf("initial belief {0,1} missing from unsolvable set %v", res.Unsolvable)
	}
	// Distinguishable observations let the policy branch... but the only
	// action still dooms w1, so it stays unsolvable for a different reason:
	// there is no safe action for w1 at all.
	res = Solve(mk("x", "y"))
	if res.Policy != nil {
		t.Fatal("expected unsolvable: w1 has no safe action")
	}
}

// A world that only loops on itself can never reach the goal.
func TestUnsolvableLoop(t *testing.T) {
	p := Problem{
		Worlds: []World{
			{Name: "s", Obs: "s"},             // 0
			{Name: "loop", Obs: "s"},          // 1
			{Name: "g", Obs: "g", Goal: true}, // 2
		},
		Actions: []Action{
			{Name: "go", Next: map[int][]int{0: {2}, 1: {1}}},
		},
		Initial: []int{0, 1},
	}
	res := Solve(p)
	if res.Policy != nil {
		t.Fatal("expected unsolvable")
	}
	if len(res.Unsolvable) == 0 {
		t.Fatal("expected unsolvable belief states")
	}
}

// Actions that may enter an unsafe world must never be chosen.
func TestUnsafeBranchExcluded(t *testing.T) {
	p := Problem{
		Worlds: []World{
			{Name: "s", Obs: "s"},                   // 0
			{Name: "g", Obs: "g", Goal: true},       // 1
			{Name: "bad", Obs: "bad", Unsafe: true}, // 2
		},
		Actions: []Action{
			{Name: "risky", Next: map[int][]int{0: {1, 2}}},
			{Name: "safe", Next: map[int][]int{0: {1}}},
		},
		Initial: []int{0},
	}
	res := Solve(p)
	if res.Policy == nil {
		t.Fatal("expected a policy")
	}
	if res.Policy.Action != "safe" {
		t.Fatalf("root action = %q, want safe (risky may enter unsafe world)", res.Policy.Action)
	}
	if res.WorstCase != 1 {
		t.Fatalf("worst case = %d, want 1", res.WorstCase)
	}
	if _, err := Verify(p, res.Policy); err != nil {
		t.Fatalf("Verify rejected solver policy: %v", err)
	}
}

// Already at the goal: empty policy, zero worst-case steps.
func TestInitiallyGoal(t *testing.T) {
	p := Problem{
		Worlds: []World{
			{Name: "g", Obs: "g", Goal: true},
		},
		Actions: []Action{},
		Initial: []int{0},
	}
	res := Solve(p)
	if res.Policy == nil {
		t.Fatal("expected an empty policy")
	}
	if res.WorstCase != 0 || res.Policy.Action != "" {
		t.Fatalf("got worst=%d action=%q, want 0 and leaf", res.WorstCase, res.Policy.Action)
	}
	worst, err := Verify(p, res.Policy)
	if err != nil || worst != 0 {
		t.Fatalf("Verify = %d, %v; want 0, nil", worst, err)
	}
}

// Verify must reject a corrupted policy (wrong branch coverage).
func TestVerifyRejectsBadPolicy(t *testing.T) {
	p := Problem{
		Worlds: []World{
			{Name: "s", Obs: "s"},
			{Name: "g", Obs: "g", Goal: true},
			{Name: "h", Obs: "h"},
		},
		Actions: []Action{
			{Name: "go", Next: map[int][]int{0: {1, 2}, 2: {1}}},
		},
		Initial: []int{0},
	}
	// Missing the "h" branch.
	bad := &Policy{Action: "go", Branches: map[string]*Policy{"g": {}}}
	if _, err := Verify(p, bad); err == nil {
		t.Fatal("Verify accepted a policy with a missing branch")
	}
	// Non-goal leaf.
	bad2 := &Policy{Action: "go", Branches: map[string]*Policy{"g": {}, "h": {}}}
	if _, err := Verify(p, bad2); err == nil {
		t.Fatal("Verify accepted a policy ending on a non-goal belief")
	}
}

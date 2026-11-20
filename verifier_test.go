package updatestrategy

import "testing"

func s1Problem() Problem {
	belief := []World{
		factoryWorld(true, New),
		factoryWorld(true, EmptyDigest),
		factoryWorld(true, UnknownDigest),
	}
	return Problem{
		InitialBelief: belief,
		Actions:       fullHostActions(),
		Target:        Target{TargetImage: New, MinCounter: 1},
	}
}

func TestVerifierDetectsTamperedPolicy(t *testing.T) {
	policy, err := Plan(s1Problem())
	if err != nil {
		t.Fatal(err)
	}
	if errs := VerifyPolicy(policy, fullHostActions()); len(errs) != 0 {
		t.Fatalf("fresh policy must verify: %v", errs)
	}

	// Tamper 1: drop one required observation branch.
	var victim *PolicyNode
	for _, n := range collectNodes(policy.Root) {
		if len(n.Branches) > 0 {
			victim = n
			break
		}
	}
	if victim == nil {
		t.Fatal("no branching node found")
	}
	var dropped Observation
	for obs := range victim.Branches {
		dropped = obs
		break
	}
	delete(victim.Branches, dropped)
	if errs := VerifyPolicy(policy, fullHostActions()); len(errs) == 0 {
		t.Fatal("verifier must detect a missing observation branch")
	}

	// Rebuild a clean policy and tamper the worst-cost field instead.
	policy2, _ := Plan(s1Problem())
	policy2.Root.WorstCost = policy2.Root.WorstCost + 99
	if errs := VerifyPolicy(policy2, fullHostActions()); len(errs) == 0 {
		t.Fatal("verifier must detect an inconsistent worst cost")
	}

	// Tamper 3: a goal belief that secretly stops while a world is not done.
	policy3, _ := Plan(s1Problem())
	policy3.Root.IsGoal = true
	policy3.Root.Action = nil
	policy3.Root.Branches = nil
	policy3.Root.WorstCost = 0
	if errs := VerifyPolicy(policy3, fullHostActions()); len(errs) == 0 {
		t.Fatal("verifier must detect premature termination")
	}
}

func TestVerifierRejectsUndeclaredAction(t *testing.T) {
	policy, err := Plan(s1Problem())
	if err != nil {
		t.Fatal(err)
	}
	declared := fullHostActions()
	if errs := VerifyPolicy(policy, declared[1:]); len(errs) == 0 {
		t.Fatal("verifier must reject a policy using an undeclared action")
	}
}

func TestExhaustiveCoversPowerLossAndGoal(t *testing.T) {
	policy, err := Plan(s1Problem())
	if err != nil {
		t.Fatal(err)
	}
	for _, start := range s1Problem().InitialBelief {
		runs, err := ExhaustiveRun(policy, start)
		if err != nil {
			t.Fatalf("exhaustive: %v", err)
		}
		sawLoss := start.PowerLost
		for _, r := range runs {
			if !r.ReachedGoal {
				t.Fatal("every enumerated path must reach the target")
			}
			if r.PowerLosses > 1 {
				t.Fatal("at most one power loss is permitted per execution")
			}
			if r.PowerLosses == 1 {
				sawLoss = true
			}
			if !r.Final.Bootable(r.Final.Active) {
				t.Fatal("every step must stay offline bootable")
			}
		}
		if !sawLoss {
			t.Fatalf("exhaustive run from %s never exercised a power-loss branch", start.ID())
		}
	}
}

func collectNodes(n *PolicyNode) []*PolicyNode {
	if n == nil {
		return nil
	}
	out := []*PolicyNode{n}
	for _, c := range n.Branches {
		out = append(out, collectNodes(c)...)
	}
	return out
}

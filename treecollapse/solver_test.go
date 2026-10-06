package main

import (
	"math/rand"
	"testing"
)

// randomInstance builds a random tree: group i (i >= 1) gets a random
// parent in [0, i), so indices always point to already-created groups.
func randomInstance(rng *rand.Rand, groups, maxLeaves, blocks, maxTargets int) *Instance {
	inst := &Instance{BlockSizes: make([]int, blocks)}
	for b := range inst.BlockSizes {
		inst.BlockSizes[b] = 1 + rng.Intn(10)
	}
	for g := 0; g < groups; g++ {
		grp := Group{
			Parent:       -1,
			ExpandCost:   1 + rng.Intn(6),
			CollapseCost: 1 + rng.Intn(3),
		}
		if g > 0 {
			grp.Parent = rng.Intn(g)
		}
		for l := 0; l < rng.Intn(maxLeaves+1); l++ {
			grp.Leaves = append(grp.Leaves, Leaf{Penalty: 1 + rng.Intn(5)})
		}
		for _, b := range rng.Perm(blocks)[:rng.Intn(blocks+1)] {
			grp.Blocks = append(grp.Blocks, b)
		}
		inst.Groups = append(inst.Groups, grp)
	}
	available := groups
	for _, g := range inst.Groups {
		available += len(g.Leaves)
	}
	if maxTargets > available {
		maxTargets = available
	}
	seen := map[Target]bool{}
	for len(inst.Targets) < maxTargets {
		g := rng.Intn(groups)
		t := Target{Group: g, Leaf: -1}
		if len(inst.Groups[g].Leaves) > 0 && rng.Intn(2) == 0 {
			t.Leaf = rng.Intn(len(inst.Groups[g].Leaves))
		}
		if !seen[t] {
			seen[t] = true
			inst.Targets = append(inst.Targets, t)
		}
	}
	return inst
}

// TestPart1BruteForceMatchesStructure checks the subset enumeration
// against the structural optimum (ancestor closure of all targets) on
// random instances with up to 12 groups.
func TestPart1BruteForceMatchesStructure(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 200; trial++ {
		inst := randomInstance(rng, 2+rng.Intn(11), 2, 4, 1+rng.Intn(4))
		res := inst.SolvePart1()
		req := inst.RequiredMask()
		expand, penalty, _ := inst.evalMask(req)
		if res.Cost != expand+penalty {
			t.Fatalf("trial %d: brute force %d != structural %d", trial, res.Cost, expand+penalty)
		}
		if !inst.closedMask(res.Mask) || !inst.allVisible(res.Mask) {
			t.Fatalf("trial %d: optimum violates closure or visibility", trial)
		}
		if res.FeasibleSets < 1 {
			t.Fatalf("trial %d: no feasible sets counted", trial)
		}
	}
}

// TestPart1TwentyGroups runs the full 2^20 enumeration once to confirm
// the size limit is handled and the result matches the structural check.
func TestPart1TwentyGroups(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	inst := randomInstance(rng, 20, 1, 3, 3)
	res := inst.SolvePart1()
	req := inst.RequiredMask()
	expand, penalty, _ := inst.evalMask(req)
	if res.Cost != expand+penalty {
		t.Fatalf("brute force %d != structural %d", res.Cost, expand+penalty)
	}
}

// TestPart2SearchMatchesVerifier cross-checks Dijkstra against the
// Bellman-style fixpoint verifier on random instances and budgets, and
// replays every plan to confirm legality, budget and target coverage.
func TestPart2SearchMatchesVerifier(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for trial := 0; trial < 300; trial++ {
		inst := randomInstance(rng, 2+rng.Intn(7), 1, 4, 1+rng.Intn(3))
		total := 0
		for _, s := range inst.BlockSizes {
			total += s
		}
		budget := rng.Intn(total + 1)
		plan := inst.SolvePart2(budget)
		vcost, vok := inst.VerifyPart2(budget)
		if plan.OK != vok || (plan.OK && plan.TotalCost != vcost) {
			t.Fatalf("trial %d: solver=(%v,%d) verifier=(%v,%d)", trial, plan.OK, plan.TotalCost, vok, vcost)
		}
		if !plan.OK {
			continue
		}
		mask := uint64(0)
		done := inst.doneTargets(0)
		cost := 0
		for _, a := range plan.Actions {
			legal := false
			for _, mv := range inst.moves(mask) {
				if mv == a {
					legal = true
				}
			}
			if !legal {
				t.Fatalf("trial %d: illegal action %+v at mask %b", trial, a, mask)
			}
			mask = apply(mask, a)
			if m := inst.memoryOf(mask); m > budget {
				t.Fatalf("trial %d: memory %d over budget %d", trial, m, budget)
			}
			done |= inst.doneTargets(mask)
			cost += a.Cost
		}
		allDone := uint64(1)<<uint(len(inst.Targets)) - 1
		if done != allDone {
			t.Fatalf("trial %d: plan misses targets, done=%b want %b", trial, done, allDone)
		}
		if cost != plan.TotalCost {
			t.Fatalf("trial %d: cost mismatch %d != %d", trial, cost, plan.TotalCost)
		}
	}
}

// TestDemoInstance pins the sample outputs used by main.
func TestDemoInstance(t *testing.T) {
	inst := demoInstance()
	res := inst.SolvePart1()
	if res.Cost != 32 || res.ExpandCost != 23 || res.LeafPenalty != 9 {
		t.Fatalf("part1 demo: got cost=%d expand=%d penalty=%d", res.Cost, res.ExpandCost, res.LeafPenalty)
	}
	plan := inst.SolvePart2(29)
	if !plan.OK || plan.TotalCost != 24 || plan.Peak != 29 {
		t.Fatalf("part2 demo: got ok=%v cost=%d peak=%d", plan.OK, plan.TotalCost, plan.Peak)
	}
	if vcost, ok := inst.VerifyPart2(29); !ok || vcost != 24 {
		t.Fatalf("part2 demo verifier: got ok=%v cost=%d", ok, vcost)
	}
}

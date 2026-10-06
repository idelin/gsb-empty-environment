// Command demo exercises both planners: a deterministic BFS plan with
// step-by-step capability sets, and a partially observable policy tree
// minimizing worst-case steps.
package main

import (
	"fmt"

	"plango"
)

func main() {
	deterministic()
	fmt.Println()
	contingent()
}

func deterministic() {
	fmt.Println("== deterministic planner (BFS + replay cross-check) ==")
	p := plango.Problem{
		Actions: []plango.Action{
			{Name: "boot", Pre: plango.Caps(), Add: plango.Caps(0)},
			{Name: "load", Pre: plango.Caps(0), Add: plango.Caps(1)},
			{Name: "calibrate", Pre: plango.Caps(1), Add: plango.Caps(2), Del: plango.Caps(1)},
			{Name: "deploy", Pre: plango.Caps(0, 2), Add: plango.Caps(3)},
			{Name: "shortcut", Pre: plango.Caps(0), Add: plango.Caps(1, 2)},
		},
		Initial:   plango.Caps(),
		Goal:      plango.Caps(3),
		Forbidden: []plango.State{plango.Caps(1, 2)},
	}
	plan, err := plango.Verify(p)
	if err != nil {
		fmt.Println("no plan:", err)
		return
	}
	for _, line := range plan.Steps() {
		fmt.Println(" ", line)
	}
	fmt.Printf("  shortest plan: %d actions, replay cross-check OK\n", len(plan.Actions))

	done := p
	done.Initial = plango.Caps(0, 2, 3)
	empty, _ := plango.Solve(done)
	fmt.Printf("  goal already satisfied initially: %d actions (empty plan)\n", len(empty.Actions))
}

func contingent() {
	fmt.Println("== contingent planner (belief-space fixpoint + full replay) ==")
	p := plango.NDProblem{
		Worlds: []plango.World{
			{State: plango.Caps(), Obs: "cold"},
			{State: plango.Caps(0), Obs: "warm"},
			{State: plango.Caps(1), Obs: "done"},
			{State: plango.Caps(2), Obs: "warm"}, // dead end posing as "warm"
		},
		Actions: []plango.NDAction{
			{Name: "charge", Trans: [][]int{{1, 2}, {1}, {2}, {}}},
			{Name: "fire", Trans: [][]int{{}, {2}, {2}, {}}},
		},
		Goal:    plango.Caps(1),
		Initial: plango.BWorlds(0, 1),
	}
	sol := plango.SolveND(p)
	if !sol.Solvable {
		fmt.Println("  unsolvable; dead worlds:", sol.DeadWorlds)
		return
	}
	fmt.Print(sol.Policy.String())
	if err := plango.VerifyPolicy(p, sol.Policy); err != nil {
		fmt.Println("  verification failed:", err)
		return
	}
	fmt.Printf("  worst-case %d steps, full belief-state replay OK; dead worlds: %v\n",
		sol.Policy.Worst, sol.DeadWorlds)

	hopeless := p
	hopeless.Initial = plango.BWorlds(1, 3) // indistinguishable, needs differ
	bad := plango.SolveND(hopeless)
	fmt.Printf("  ambiguous initial belief {w1 w3}: solvable=%v, dead worlds=%v\n",
		bad.Solvable, bad.DeadWorlds)
}

// Command demo exercises both planners on small examples and prints the
// step-by-step results.
package main

import (
	"fmt"

	"planner/belief"
	"planner/caps"
)

func main() {
	// Part 1: deterministic capability planning with revocation.
	p1 := caps.Problem{
		Goal: caps.Cap(2),
		Actions: []caps.Action{
			{Name: "grantA", Add: caps.Cap(0)},
			{Name: "grantB", Require: caps.Cap(0), Add: caps.Cap(1)},
			{Name: "revokeA", Require: caps.Cap(1), Del: caps.Cap(0)},
			{Name: "unlock", Require: caps.Cap(1), Forbid: caps.Cap(0), Add: caps.Cap(2)},
		},
	}
	sol, ok := caps.Solve(p1)
	if !ok {
		fmt.Println("part 1: no plan")
	} else {
		if _, err := caps.Replay(p1, sol.Actions); err != nil {
			fmt.Println("part 1: replay failed:", err)
			return
		}
		fmt.Println("part 1: shortest plan (verified by replay)")
		for i, a := range sol.Actions {
			fmt.Printf("  step %d: %-8s -> %v\n", i+1, a.Name, sol.States[i+1])
		}
	}

	// Part 2: partially observable planning over a finite set of worlds.
	p2 := belief.Problem{
		Worlds: []belief.World{
			{Name: "ok", Obs: "start"},
			{Name: "flaky", Obs: "start"},
			{Name: "done", Obs: "done", Goal: true},
			{Name: "broken", Obs: "oops"},
		},
		Actions: []belief.Action{
			{Name: "try", Next: map[int][]int{0: {2}, 1: {3}}},
			{Name: "fix", Next: map[int][]int{3: {2}}},
		},
		Initial: []int{0, 1},
	}
	res := belief.Solve(p2)
	if res.Policy == nil {
		fmt.Println("part 2: unsolvable, dead belief states:", res.Unsolvable)
	} else {
		worst, err := belief.Verify(p2, res.Policy)
		fmt.Printf("part 2: policy (worst case %d steps, verify=%d, err=%v)\n", res.WorstCase, worst, err)
		var print func(n *belief.Policy, indent string)
		print = func(n *belief.Policy, indent string) {
			if n.Action == "" {
				fmt.Println(indent + "goal")
				return
			}
			fmt.Println(indent + "do " + n.Action)
			for obs, child := range n.Branches {
				fmt.Printf("%s  on %q:\n", indent, obs)
				print(child, indent+"    ")
			}
		}
		print(res.Policy, "  ")
	}
}

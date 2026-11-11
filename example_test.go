package netsec

import "fmt"

func ExampleSolve() {
	problem := &Problem{
		Nodes: []Node{
			{ID: "phish", Gate: GateAND},
			{ID: "creds", Gate: GateAND},
			{ID: "session", Gate: GateAND, Preconditions: []string{"phish", "creds"}},
			{ID: "pivot", Gate: GateAND, Preconditions: []string{"session"}},
			{ID: "breach", Gate: GateAND, Preconditions: []string{"pivot"}},
		},
		Target: "breach",
		Patches: []Patch{
			{ID: "mfa", Action: "creds", Cost: 1},
			{ID: "filter", Action: "phish", Cost: 2},
			{ID: "segment", Action: "pivot", Cost: 7},
		},
	}
	solution, err := Solve(problem)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("selected:", solution.Selected, "cost:", solution.Cost)
	fmt.Println("target unreachable:", solution.Proof.Blocking.ReachableClosure)
	for _, w := range solution.Proof.Minimality {
		fmt.Println("omit", w.OmittedPatch, "->", w.ActivationOrder)
	}
	// Output:
	// selected: [mfa] cost: 1
	// target unreachable: [phish]
	// omit mfa -> [phish creds session pivot breach]
}

package netsec_test

import (
	"fmt"

	"example.org/netsec/q0120"
)

func ExamplePlan() {
	req := &netsec.Request{
		Edges: []netsec.Edge{
			{EdgeID: "e0", From: "host", To: "switch"},
			{EdgeID: "e1", From: "switch", To: "quarantine"},
		},
		Initial: map[string]bool{"e0": true, "e1": false},
		Goal:    map[string]bool{"e0": false, "e1": true},
		ForbiddenReach: []netsec.ForbiddenPair{
			{From: "host", To: "quarantine"},
		},
	}

	result, err := netsec.Plan(req)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	if !result.Feasible {
		fmt.Println("infeasible:", result.Reason)
		fmt.Println("safe states examined:", len(result.Evidence.VisitedSafe))
		return
	}

	for _, step := range result.Steps {
		fmt.Printf("flip %s -> enabled=%v\n", step.EdgeID, step.Enabled)
	}

	if ok, err := netsec.Verify(req, result); err != nil || !ok {
		fmt.Println("verification failed")
		return
	}
	fmt.Println("verified")
	// Output:
	// flip e0 -> enabled=false
	// flip e1 -> enabled=true
	// verified
}

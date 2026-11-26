package bgpsim_test

import (
	"fmt"

	bgpsim "prescreen.local/ps-b15ac20600"
)

// ExampleSimulate_medTriangle expands the canonical three-reflector MED
// triangle round by round. The system alternates between rounds 1 and 2,
// so round 3 repeats round 1 with minimum period 2.
func ExampleSimulate_medTriangle() {
	res, err := bgpsim.Simulate(triangleConfig(false))
	if err != nil {
		panic(err)
	}
	for i := range res.Rounds {
		fmt.Printf("round %d: %v\n", i, rrVector(res, i))
	}
	osc := res.Oscillation
	fmt.Printf("status=%s first=%d repeat=%d period=%d\n",
		res.Status, osc.FirstRound, osc.RepeatRound, osc.Period)

	// Output:
	// round 0: [- - -]
	// round 1: [b1 b2 b3]
	// round 2: [b2 b3 b1]
	// round 3: [b1 b2 b3]
	// status=oscillating first=1 repeat=3 period=2
}

// ExampleSimulate_alwaysCompareMED shows the same topology converging once
// MED is compared across neighboring AS boundaries.
func ExampleSimulate_alwaysCompareMED() {
	res, err := bgpsim.Simulate(triangleConfig(true))
	if err != nil {
		panic(err)
	}
	for i := range res.Rounds {
		fmt.Printf("round %d: %v\n", i, rrVector(res, i))
	}
	fmt.Printf("status=%s converged=%v winner=%s\n",
		res.Status, res.Converged, res.BestRoute("rr2", trianglePrefix).Egress)

	// Output:
	// round 0: [- - -]
	// round 1: [b1 b2 b3]
	// round 2: [b1 b1 b1]
	// round 3: [b1 b1 b1]
	// status=converged converged=true winner=b1
}

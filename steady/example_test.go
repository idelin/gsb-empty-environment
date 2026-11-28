package steady_test

import (
	"fmt"
	"time"

	"prescreen.local/ps-9f073c0c88/steady"
)

func ExampleSolve() {
	curve := steady.SoftThresholdCurve{
		BaseFailure: 0.02, SoftLoad: 45, HardLoad: 70, HardFailure: 0.9,
	}
	nodes := []steady.NodeConfig{{
		Name: "D", Capacity: 120,
		MeanServiceTime: 30 * time.Millisecond, MinServiceTime: 5 * time.Millisecond,
		Curve: curve,
	}}
	// A single self-contained node entry with no edges demonstrates the
	// result structure; real chains wire A->B->C->D edges here.
	top := &steady.Topology{Nodes: nodes, Ingress: map[string]float64{"D": 40}}
	res, err := steady.Solve(top)
	if err != nil {
		panic(err)
	}
	for _, n := range res.Nodes {
		fmt.Printf("%s physical=%.2f pFail=%.3f overCapacity=%v\n",
			n.Name, n.PhysicalArrival, n.FailureRate, n.OverCapacity)
	}
	// Output:
	// D physical=40.00 pFail=0.020 overCapacity=false
}

package netsec_test

import (
	"fmt"

	"example.org/netsec/uid1237"
)

func ExampleAnalyze() {
	spec := netsec.Spec{
		Locks: []string{"A", "B"},
		Threads: [2]netsec.Thread{
			{
				Name: "T0", Start: "0", States: []string{"0", "1", "2"},
				Edges: []netsec.Edge{
					{Name: "lockA", From: "0", To: "1", Op: netsec.OpLock, Lock: "A"},
					{Name: "lockB", From: "1", To: "2", Op: netsec.OpLock, Lock: "B"},
				},
			},
			{
				Name: "T1", Start: "0", States: []string{"0", "1", "2"},
				Edges: []netsec.Edge{
					{Name: "lockB", From: "0", To: "1", Op: netsec.OpLock, Lock: "B"},
					{Name: "lockA", From: "1", To: "2", Op: netsec.OpLock, Lock: "A"},
				},
			},
		},
	}

	res, err := netsec.Analyze(spec)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("reachable:", res.Reachable)
	fmt.Println("kind:", res.Kind)
	for _, step := range res.Trace {
		fmt.Printf("%d. %s:%s\n", step.Seq, step.Thread, step.Edge)
	}
	// Output:
	// reachable: true
	// kind: cycle
	// 1. T0:lockA
	// 2. T1:lockB
}

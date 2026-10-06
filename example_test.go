package flowshop_test

import (
	"fmt"

	"flowshop"
)

func ExampleSolve() {
	jobs := []flowshop.Job{
		{ID: 1, P1: 4, P2: 2},
		{ID: 2, P1: 1, P2: 5},
		{ID: 3, P1: 3, P2: 3},
	}
	res, _ := flowshop.Solve(jobs, nil, nil)
	fmt.Println("order:", res.Sequence)
	fmt.Println("makespan:", res.Schedule.Makespan)
	for _, b := range res.Schedule.Blocking {
		fmt.Printf("job %d blocks M1 during [%d,%d)\n", b.JobID, b.Start, b.End)
	}
	// Output:
	// order: [2 1 3]
	// makespan: 12
	// job 1 blocks M1 during [5,6)
}

func ExampleReplan() {
	// Jobs 2 and 3 already started in this order and cannot be
	// re-sequenced; machine 2 is down during [8,10).
	prefix := []flowshop.Job{{ID: 2, P1: 4, P2: 1}, {ID: 3, P1: 2, P2: 2}}
	remaining := []flowshop.Job{{ID: 1, P1: 3, P2: 4}, {ID: 4, P1: 2, P2: 3}}
	down2 := []flowshop.Interval{{Start: 8, End: 10}}
	res, _ := flowshop.Replan(prefix, remaining, nil, down2)
	fmt.Println("order:", res.Sequence)
	fmt.Println("makespan:", res.Schedule.Makespan)
	for _, d := range res.Schedule.Downtime {
		fmt.Printf("job %d paused on M%d during [%d,%d)\n", d.JobID, d.Machine, d.Start, d.End)
	}
	// Output:
	// order: [2 3 1 4]
	// makespan: 17
	// job 1 paused on M2 during [9,10)
}

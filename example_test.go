package netsec_test

import (
	"fmt"

	netsec "example.org/netsec/q0374"
)

func ExamplePlan() {
	// Bit 2 is a lock. The permission write is valid only before the lock is
	// set, so the shortest safe plan is grant then lock.
	problem := netsec.Problem{
		Bits:     3,
		Initial:  0,
		Target:   0b101,
		LockMask: 0b100,
		Ops: []netsec.Operation{
			{Name: "lock", Mask: 0b100, Value: 0b100},
			{Name: "grant", Mask: 0b001, Value: 0b001,
				RequireMask: 0b100, RequireValue: 0},
		},
	}

	result, err := netsec.Plan(problem)
	if err != nil {
		panic(err)
	}
	for _, step := range result.Steps {
		fmt.Printf("%s: %#03b -> %#03b\n", step.Name, step.From, step.To)
	}
	// Output:
	// grant: 0b000 -> 0b001
	// lock: 0b001 -> 0b101
}

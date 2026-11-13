package netsec_test

import (
	"fmt"

	"example.org/netsec/q0032"
)

// ExampleReconstruct demonstrates that two frame slots naming one virtual
// object receive the same concrete identity.
func ExampleReconstruct() {
	in := netsec.Reconstruction{
		Registers: []netsec.Value{netsec.VirtualRef(7)},
		Stack:     []netsec.Value{netsec.VirtualRef(7)},
		Objects: []netsec.VirtualObject{{
			ID: 7,
			Fields: map[string]netsec.Value{
				"value": netsec.IntValue(42),
			},
		}},
	}

	frame, proof, err := netsec.Reconstruct(in, netsec.NewHeap())
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(frame.Registers[0].Heap == frame.Stack[0].Heap)
	fmt.Println(proof[7] == frame.Registers[0].Heap)

	// Output:
	// true
	// true
}

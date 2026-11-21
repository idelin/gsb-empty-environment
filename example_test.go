package admission_test

import (
	"fmt"

	admission "prescreen.local/ddd-10548-v2"
)

func Example() {
	graph := admission.Graph{
		Roots:    []string{"vol"},
		Physical: []admission.PhysicalRange{{Start: 100, Length: 4, Permission: admission.PermReadWrite}},
		Volumes: []admission.Volume{{
			Name:     "vol",
			Length:   4,
			Segments: []admission.Segment{admission.Physical(0, 4, admission.PermReadWrite, 100)},
		}},
	}
	snap, err := admission.Compile(graph)
	if err != nil {
		panic(err)
	}
	handle, err := snap.Handle("vol")
	if err != nil {
		panic(err)
	}
	proof, writable, err := handle.CheckWrite(0, 4)
	if err != nil {
		panic(err)
	}
	fmt.Println(proof.Version, writable, proof.Claims[0].Physical.Start)
	// Output: 0 true 100
}

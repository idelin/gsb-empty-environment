package genring_test

import (
	"fmt"

	"example.org/netsec/q0317/genring"
)

func ExampleRing() {
	var released []genring.Lease
	ring, err := genring.New(genring.Config{
		Size:    16,
		TagBits: genring.SafeTagBits(16),
		OnRelease: func(lease genring.Lease, tenant genring.Tenant, gen genring.Generation, reason genring.ReleaseReason) {
			released = append(released, lease)
		},
	})
	if err != nil {
		panic(err)
	}

	// Bind a buffer lease and tenant to a slot, then program the descriptor
	// with the slot index and the truncated tag the hardware will echo.
	index, gen, err := ring.Submit(42, 7)
	if err != nil {
		panic(err)
	}
	descriptorTag := ring.Tag(gen)

	// Hardware completion returns the slot index and truncated generation.
	result, err := ring.Complete(index, descriptorTag)
	if err != nil {
		panic(err)
	}
	fmt.Printf("attributed slot=%d gen=%d released=%v leases=%v\n",
		result.Slot, result.Generation.Sequence(), result.Released, released)
	// Output: attributed slot=0 gen=1 released=true leases=[42]
}

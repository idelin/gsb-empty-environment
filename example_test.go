package netsec_test

import (
	"fmt"

	netsec "example.org/netsec/q0352"
)

// Example shows a Go sealing host driving one full window: partitions fill
// continuous prefixes, a gap is rejected until filled, and the barrier only
// signs the aggregate root once every manifest partition is complete.
func Example() {
	manifest := netsec.Manifest{
		Window: 1,
		Partitions: []netsec.PartitionSpec{
			{Partition: "orders", UpperBound: 2},
			{Partition: "payments", UpperBound: 1},
		},
	}
	barrier, err := netsec.NewBarrier(manifest)
	if err != nil {
		panic(err)
	}
	key, err := netsec.GenerateEd25519Key("host-root-key")
	if err != nil {
		panic(err)
	}

	// "orders" sends seq 2 first: the barrier keeps the gap open.
	orders1 := netsec.NewSeal(1, "orders", 1, netsec.Digest{})
	orders2 := netsec.NewSeal(1, "orders", 2, orders1.Digest)
	if _, err := barrier.Submit(orders2); err != nil {
		fmt.Println("gap rejected:", err != nil)
	}

	// Fill the gap, then complete both partitions.
	if _, err := barrier.Submit(orders1); err != nil {
		panic(err)
	}
	if _, err := barrier.Submit(orders2); err != nil {
		panic(err)
	}
	payment1 := netsec.NewSeal(1, "payments", 1, netsec.Digest{})
	if _, err := barrier.Submit(payment1); err != nil {
		panic(err)
	}

	attestation, err := barrier.Finalize(key)
	if err != nil {
		panic(err)
	}

	verifier, err := netsec.NewEd25519Verifier(key.KeyID(), key.PublicKey())
	if err != nil {
		panic(err)
	}
	fmt.Println("signed:", attestation.Signed())
	fmt.Println("verified:", attestation.Verify(&manifest, verifier) == nil)
	fmt.Println("partitions:", len(attestation.Roots))

	// Output:
	// gap rejected: true
	// signed: true
	// verified: true
	// partitions: 2
}

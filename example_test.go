package rga_test

import (
	"fmt"

	"rga"
)

// Two replicas edit the same text offline, then merge in arbitrary order.
func Example() {
	alice := rga.New("alice")
	bob := rga.New("bob")

	// Alice types "hi" while offline.
	a1, _ := alice.InsertAt(0, 'h')
	a2, _ := alice.InsertAt(1, 'i')

	// Bob has seen nothing yet and concurrently prepends 'H'.
	b1, _ := bob.InsertAt(0, 'H')

	// Merge in both directions; delivery order does not matter.
	_ = bob.Apply(a1)
	_ = bob.Apply(a2)
	_ = alice.Apply(b1)

	// Concurrent inserts at the head are ordered by operation ID:
	// (bob,1) sorts before (alice,1).
	fmt.Println(alice.String())
	fmt.Println(bob.String())

	// Alice deletes 'h'. The tombstone stays as an anchor, so 'i',
	// which was inserted after 'h', keeps its place.
	d1, _ := alice.DeleteAt(1)
	_ = bob.Apply(d1)
	fmt.Println(alice.String())
	fmt.Println(bob.String())

	// Output:
	// Hhi
	// Hhi
	// Hi
	// Hi
}

package rga_test

import (
	"fmt"

	"rga"
)

// Two users co-edit the document "AB". User 1 inserts 'X' after 'A' while
// user 2 concurrently inserts 'Y' after 'A'. After exchanging operations,
// both replicas converge to the same document.
func Example() {
	user1, user2 := rga.New("user1"), rga.New("user2")

	// User 1 types the initial document; user 2 receives it.
	opA := user1.InsertAfter(rga.ID{}, 'A')
	opB := user1.InsertAfter(opA.ID, 'B')
	user2.Apply(opA)
	user2.Apply(opB)

	// Concurrent inserts after 'A'.
	opX := user1.InsertAfter(opA.ID, 'X') // user 1, offline
	opY := user2.InsertAfter(opA.ID, 'Y') // user 2, offline

	// Merge.
	user2.Apply(opX)
	user1.Apply(opY)

	fmt.Println("user1 sees:", user1.String())
	fmt.Println("user2 sees:", user2.String())
	// Output:
	// user1 sees: AYXB
	// user2 sees: AYXB
}

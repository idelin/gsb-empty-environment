// Command demo simulates the acceptance scenario: two users co-editing the
// document "AB", concurrently inserting after 'A', then merging.
package main

import (
	"fmt"

	"rga"
)

func main() {
	user1 := rga.New("user1")
	user2 := rga.New("user2")

	// Shared initial document "AB".
	opA := user1.InsertAfter(rga.ID{}, 'A')
	opB := user1.InsertAfter(opA.ID, 'B')
	user2.Apply(opA)
	user2.Apply(opB)
	fmt.Printf("initial:  user1=%q user2=%q\n", user1.String(), user2.String())

	// Both users insert after 'A' at the same time (no sync in between).
	opX := user1.InsertAfter(opA.ID, 'X')
	opY := user2.InsertAfter(opA.ID, 'Y')
	fmt.Printf("offline:  user1=%q user2=%q\n", user1.String(), user2.String())

	// Merge: exchange operations.
	user2.Apply(opX)
	user1.Apply(opY)
	fmt.Printf("merged:   user1=%q user2=%q\n", user1.String(), user2.String())

	if user1.String() == user2.String() {
		fmt.Println("converged: both replicas show the same document")
	} else {
		fmt.Println("ERROR: replicas diverged")
	}
}

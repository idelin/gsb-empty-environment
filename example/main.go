// Command example simulates the acceptance scenario: two users edit the
// document "AB"; user 1 inserts 'X' after 'A' while user 2 concurrently
// inserts 'Y' after 'A'. After merging, both replicas print the same text.
package main

import (
	"fmt"
	"log"

	"rga"
)

func main() {
	// User 1 types the initial document "AB".
	user1 := rga.New(1)
	if _, err := user1.InsertAfter(rga.RootID, 'A'); err != nil {
		log.Fatal(err)
	}
	idA, err := user1.IDAt(0)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := user1.InsertAfter(idA, 'B'); err != nil {
		log.Fatal(err)
	}

	// The initial document is replicated to user 2.
	user2 := rga.New(2)
	for _, op := range user1.Ops() {
		if err := user2.Apply(op); err != nil {
			log.Fatal(err)
		}
	}
	fmt.Printf("initial: user1=%q user2=%q\n", user1.String(), user2.String())

	// Concurrent edits at the same position (after 'A').
	opX, err := user1.InsertAfter(idA, 'X') // user 1 inserts 'X'
	if err != nil {
		log.Fatal(err)
	}
	opY, err := user2.InsertAfter(idA, 'Y') // user 2 inserts 'Y'
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("offline:  user1=%q user2=%q\n", user1.String(), user2.String())

	// The replicas exchange operations and must converge.
	if err := user2.Apply(opX); err != nil {
		log.Fatal(err)
	}
	if err := user1.Apply(opY); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("merged:   user1=%q user2=%q\n", user1.String(), user2.String())

	if user1.String() != user2.String() {
		log.Fatalf("diverged: %q != %q", user1.String(), user2.String())
	}
	fmt.Println("converged: both replicas show the same document")
}

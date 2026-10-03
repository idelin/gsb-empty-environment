package lseq_test

import (
	"fmt"
	"math/rand"

	"lseq"
)

// Example demonstrates two replicas editing concurrently and converging
// after exchanging operations.
func Example() {
	alice := lseq.New(1, rand.New(rand.NewSource(1)))
	bob := lseq.New(2, rand.New(rand.NewSource(2)))

	var ops []lseq.Op
	for _, r := range "hi" {
		ops = append(ops, alice.Insert(alice.Len(), r))
	}
	for _, r := range "yo" {
		ops = append(ops, bob.Insert(bob.Len(), r))
	}

	merged := lseq.New(3, nil)
	for _, op := range ops {
		merged.Apply(op)
	}
	fmt.Println(len(merged.String()) == 4 && merged.Sorted())
	// Output: true
}

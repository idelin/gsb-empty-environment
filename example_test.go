package seqcrdt_test

import (
	"fmt"
	"strconv"

	"seqcrdt"
)

// Two replicas edit the same text offline, then merge. The result does
// not depend on the order in which operations arrive.
func Example() {
	alice := seqcrdt.New("alice")
	bob := seqcrdt.New("bob")

	// Alice types "hi".
	opA1, _ := alice.InsertAt(0, 'h')
	opA2, _ := alice.InsertAt(1, 'i')
	// Bob sees "hi".
	_ = bob.Apply(opA1)
	_ = bob.Apply(opA2)

	// Offline: Alice deletes 'h'; concurrently Bob appends '!'.
	opA3, _ := alice.DeleteAt(0)
	opB1, _ := bob.InsertAt(2, '!')

	// Exchange ops in the "wrong" order: Bob gets the delete before
	// Alice's second insert; Alice gets Bob's insert first. Pending
	// buffering and tombstone anchors make both converge.
	_ = bob.Apply(opA3)
	_ = alice.Apply(opB1)
	_ = alice.Apply(opA3) // duplicate delivery: idempotent
	_ = bob.Apply(opA2)   // duplicate delivery: idempotent

	fmt.Println("alice:", alice.Text())
	fmt.Println("bob:", bob.Text())
	fmt.Println("converged:", alice.Text() == bob.Text())

	// Output:
	// alice: i!
	// bob: i!
	// converged: true
}

// Out-of-order delivery is buffered until dependencies arrive.
func ExampleDoc_Pending() {
	d := seqcrdt.New("viewer")

	ins := seqcrdt.Op{ID: seqcrdt.ID{Replica: "a", Seq: 1}, Kind: seqcrdt.OpInsert, Parent: seqcrdt.RootID, Value: 'a'}
	del := seqcrdt.Op{ID: seqcrdt.ID{Replica: "b", Seq: 1}, Kind: seqcrdt.OpDelete, Target: ins.ID}

	_ = d.Apply(del) // target unknown yet: buffered
	fmt.Println("pending:", len(d.Pending()))
	_ = d.Apply(ins) // now the buffered delete fires
	fmt.Println("pending:", len(d.Pending()))
	fmt.Println("text:", strconv.Quote(d.Text()))

	// Output:
	// pending: 1
	// pending: 0
	// text: ""
}

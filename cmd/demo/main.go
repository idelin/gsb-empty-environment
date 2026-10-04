// Command demo runs the snapshot solver on a cyclic transaction
// association and prints the resulting cut plus one reason chain.
package main

import (
	"fmt"

	"snapshot"
)

func main() {
	// Txn A writes p0#0 and p1#1; txn B writes p0#1 and p1#0.
	// The associations form a cycle: on p0 A precedes B, on p1 B precedes A.
	// The watermark admits all of p0 but only B@p1#0, cutting A in half.
	s, err := snapshot.Solve(snapshot.Problem{
		Partitions: [][]string{
			{"A", "B"},
			{"B", "A"},
		},
		Watermark: []int{2, 1},
	})
	if err != nil {
		panic(err)
	}

	fmt.Println("consistent frontier:", s.Frontier)
	fmt.Println("rollback events:")
	for _, ev := range s.Events {
		fmt.Printf("  #%d txn %s split -> partition %d frontier %d -> %d (witness %v)\n",
			ev.ID, ev.Txn, ev.Partition, ev.FromFrontier, ev.ToFrontier, ev.Witness)
	}
	fmt.Println("reason chain for p1#0:")
	fmt.Println(s.Explain(snapshot.VersionID{Partition: 1, Index: 0}))
}

// Command aedemo demonstrates an anti-entropy sync run between two
// diverged replicas and prints the communication statistics.
package main

import (
	"fmt"

	ae "antientropy"
)

func main() {
	a, b := ae.NewStore(), ae.NewStore()
	for i := 0; i < 1000; i++ {
		k := fmt.Sprintf("key-%04d", i)
		a.Put(k, []byte("v1"), 1)
		b.Put(k, []byte("v1"), 1)
	}
	// Diverge: updates, unique keys and deletes on both sides.
	for i := 0; i < 50; i++ {
		a.Put(fmt.Sprintf("key-%04d", i*3), []byte("a-wins"), 2)
	}
	for i := 0; i < 20; i++ {
		b.Delete(fmt.Sprintf("key-%04d", 900+i), 2)
	}
	a.Delete("key-0007", 3)
	b.Put("only-on-b", []byte("hello"), 1)

	st := ae.Sync(a, b, ae.DefaultConfig)
	fmt.Printf("converged: %d records on each side\n", a.Len())
	fmt.Printf("rounds=%d digest-msgs=%d record-msgs=%d records=%d subtrees-skipped=%d\n",
		st.Rounds, st.DigestExchanges, st.RecordExchanges,
		st.RecordsTransferred, st.NodesSkipped)
}

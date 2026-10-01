// Command demo runs the acceptance scenario against the MVCC store.
package main

import (
	"errors"
	"fmt"
	"log"

	"mvcc"
)

func main() {
	store := mvcc.NewStore(9) // seed txn gets 9, so T1=10, T2=11, T3=12

	seed := store.Begin()
	must(seed.Put("K1", "old"))
	must(seed.Commit())
	fmt.Printf("seed txn %d committed K1=%q\n", seed.ID(), "old")

	// T1 (ID=10) modifies K1, not yet committed.
	t1 := store.Begin()
	must(t1.Put("K1", "new"))
	fmt.Printf("T%d wrote K1=%q (uncommitted)\n", t1.ID(), "new")

	// T2 (ID=11) reads K1: snapshot hides T1's pending write.
	t2 := store.Begin()
	v, err := t2.Get("K1")
	must(err)
	fmt.Printf("T%d read K1=%q (sees old committed version)\n", t2.ID(), v)

	// T2 writes K1: write-write conflict -> aborted.
	err = t2.Put("K1", "t2-value")
	if errors.Is(err, mvcc.ErrWriteConflict) {
		fmt.Printf("T%d write K1 -> conflict, aborted: %v\n", t2.ID(), err)
	} else {
		log.Fatalf("expected write conflict, got %v", err)
	}

	// T1 commits; new T3 (ID=12) reads the latest committed value.
	must(t1.Commit())
	fmt.Printf("T%d committed\n", t1.ID())
	t3 := store.Begin()
	v, err = t3.Get("K1")
	must(err)
	fmt.Printf("T%d read K1=%q (sees T1's committed value)\n", t3.ID(), v)
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

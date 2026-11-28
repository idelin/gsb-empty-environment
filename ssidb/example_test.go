package ssidb_test

import (
	"errors"
	"fmt"

	"prescreen.local/ps-28e0491517/ssidb"
)

// Example demonstrates the importable API: concurrent SSI transactions,
// serialization-failure handling and a retry loop.
func Example() {
	e := ssidb.NewEngine(ssidb.DefaultConfig())

	setup := e.Begin()
	_ = setup.Put("alice", "on")
	_ = setup.Put("bob", "on")
	if err := setup.Commit(); err != nil {
		panic(err)
	}

	// Two transactions read both rows, then each disables its own row. This is
	// the canonical write-skew 2-cycle; SSI aborts exactly one of them.
	// Interleave the two transactions deterministically so their rw edges
	// form a 2-cycle. Each reads both rows, then writes its own row.
	t1 := e.Begin()
	t2 := e.Begin()
	mustGet := func(t *ssidb.Txn, key string) {
		if _, _, err := t.Get(key); err != nil {
			panic(err)
		}
	}
	mustGet(t1, "alice")
	mustGet(t1, "bob")
	mustGet(t2, "alice")
	mustGet(t2, "bob")
	if err := t1.Put("alice", "off"); err != nil {
		panic(err)
	}
	t2WriteErr := t2.Put("bob", "off")

	committed, aborted := 0, 0
	if err := t1.Commit(); errors.Is(err, ssidb.ErrSerialization) {
		aborted++
	} else if err != nil {
		panic(err)
	} else {
		committed++
	}
	if t2WriteErr != nil {
		if !errors.Is(t2WriteErr, ssidb.ErrSerialization) {
			panic(t2WriteErr)
		}
		aborted++
		_ = t2.Rollback()
	} else if err := t2.Commit(); errors.Is(err, ssidb.ErrSerialization) {
		aborted++
	} else if err != nil {
		panic(err)
	} else {
		committed++
	}
	fmt.Printf("committed=%d aborted=%d\n", committed, aborted)

	// A read-only deferrable transaction blocks until a safe snapshot exists,
	// then commits without taking predicate locks or risking an abort.
	ro := e.BeginReadOnlyDeferrable()
	rows, err := ro.Scan("a", "z")
	if err != nil {
		panic(err)
	}
	if err := ro.Commit(); err != nil {
		panic(err)
	}
	fmt.Printf("safe-ro rows=%d safe=%v\n", len(rows), ro.Safe())

	// Output:
	// committed=1 aborted=1
	// safe-ro rows=2 safe=true
}

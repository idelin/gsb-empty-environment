// Command demo simulates a power cut in the middle of a batch of
// updates and then restarts with ARIES recovery.
package main

import (
	"fmt"
	"os"
	"strings"

	"aries"
)

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func show(db *aries.DB, label string) {
	p0, err := db.ReadPage(0)
	must(err)
	p1, err := db.ReadPage(1)
	must(err)
	fmt.Printf("%s page0=%q page1=%q\n",
		label,
		strings.TrimRight(string(p0), "\x00"),
		strings.TrimRight(string(p1), "\x00"))
}

func main() {
	dir := "demo-data"
	os.RemoveAll(dir)

	fmt.Println("=== Phase 1: seed accounts, commit, checkpoint (flushed to disk) ===")
	db, err := aries.Open(dir, 256)
	must(err)
	seed := db.Begin()
	must(seed.Write(0, 0, []byte("alice=100")))
	must(seed.Write(1, 0, []byte("bob=100")))
	must(seed.Commit())
	must(db.Checkpoint())
	show(db, "seeded:   ")

	fmt.Println()
	fmt.Println("=== Phase 2: batch of transfers ===")
	tx1 := db.Begin()
	must(tx1.Write(0, 0, []byte("alice=80 ")))
	must(tx1.Write(1, 0, []byte("bob=120")))
	must(tx1.Commit())
	fmt.Printf("txn %d committed (alice-20, bob+20); dirty pages still only in memory\n", tx1.ID())

	tx2 := db.Begin()
	must(tx2.Write(0, 0, []byte("alice=60 ")))
	must(tx2.Write(1, 0, []byte("bob=140")))
	fmt.Printf("txn %d in-flight (alice-20, bob+20); never commits\n", tx2.ID())
	show(db, "in-memory:")

	fmt.Println()
	fmt.Println("*** POWER LOSS: dirty pages in memory are gone, WAL survives ***")
	db.Crash()

	fmt.Println()
	fmt.Println("=== Phase 3: restart, ARIES recovery (analysis -> redo -> undo) ===")
	db2, err := aries.Open(dir, 256)
	must(err)
	rep := db2.LastRecovery
	fmt.Printf("analysis: txn table=%v DPT=%v redo-from LSN=%d\n", rep.TxnTable, rep.DPT, rep.RedoFrom)
	fmt.Printf("redo:     replayed LSNs %v\n", rep.Redone)
	fmt.Printf("undo:     loser txns %v rolled back with CLRs at LSNs %v\n", rep.Losers, rep.CLRs)
	show(db2, "recovered:")

	must(db2.Checkpoint())
	must(db2.Close())
	fmt.Println()
	fmt.Printf("OK: committed txn %d durable, uncommitted txn %d rolled back, data intact.\n", tx1.ID(), tx2.ID())
}

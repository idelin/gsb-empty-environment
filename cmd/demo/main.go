// Command demo builds the acceptance WAL (2 committed + 3 uncommitted
// transactions, crash after a checkpoint), simulates a power failure, and
// runs the ARIES three-phase recovery, printing every step.
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"aries"
)

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func main() {
	dir, err := os.MkdirTemp("", "aries-demo-*")
	check(err)
	defer os.RemoveAll(dir)
	inj := aries.NewFaultInjector()

	wal, err := aries.OpenWAL(filepath.Join(dir, "wal.log"), inj)
	check(err)
	disk, err := aries.OpenPageStore(filepath.Join(dir, "data.db"), inj)
	check(err)
	tm := aries.NewTxnManager(wal, disk)

	fmt.Println("=== Phase 0: normal operation ===")
	must(tm.Begin(1))
	must(tm.Update(1, 1, []byte("A1")))
	must(tm.Update(1, 2, []byte("B1")))
	must(tm.Commit(1, true)) // committed + END
	must(tm.Begin(2))
	must(tm.Update(2, 3, []byte("C2")))
	must(tm.Commit(2, false)) // committed, END missing at crash
	must(tm.FlushPage(1))
	must(tm.FlushPage(2))
	must(tm.Checkpoint())
	must(tm.Begin(3))
	must(tm.Update(3, 1, []byte("A3"))) // uncommitted
	must(tm.Begin(4))
	must(tm.Update(4, 4, []byte("D4")))
	must(tm.Abort(4, 1)) // aborting: one CLR written, crash mid-abort
	must(tm.Begin(5))
	must(tm.Update(5, 2, []byte("B5")))
	must(tm.Update(5, 5, []byte("E5")))
	fmt.Println("T1 committed+END, T2 committed(no END), T3 running, T4 aborting, T5 running")
	fmt.Println("checkpoint taken; pages 1,2 flushed; *** POWER FAILURE ***")
	check(wal.Close())
	check(disk.Close())

	// ---- reboot ----
	wal2, err := aries.OpenWAL(filepath.Join(dir, "wal.log"), inj)
	check(err)
	disk2, err := aries.OpenPageStore(filepath.Join(dir, "data.db"), inj)
	check(err)
	defer wal2.Close()
	defer disk2.Close()

	fmt.Println("\n=== WAL at crash time ===")
	recs, err := wal2.Scan()
	check(err)
	for _, r := range recs {
		fmt.Println(" ", r)
	}

	eng := aries.NewEngine(wal2, disk2)

	fmt.Println("\n=== Phase 1: Analysis ===")
	check(eng.Analysis())
	fmt.Print(eng.Describe())

	fmt.Println("=== Phase 2: Redo (repeat history) ===")
	check(eng.Redo())
	fmt.Printf("re-applied %d log records\n", eng.Redone)

	fmt.Println("\n=== Phase 3: Undo (loser rollback with CLRs) ===")
	check(eng.Undo())
	fmt.Printf("undone updates=%d  CLRs written=%d  ENDs written=%d\n",
		eng.Undone, eng.CLRsWritten, eng.EndsWritten)

	fmt.Println("\n=== Final durable state ===")
	for id := uint64(1); id <= 5; id++ {
		page, err := disk2.ReadPage(id)
		check(err)
		fmt.Printf("page %d = %q\n", id, string(trimZeros(page.Data)))
	}
	fmt.Println("\n=== WAL after recovery (tail) ===")
	recs, err = wal2.Scan()
	check(err)
	for _, r := range recs[len(recs)-eng.CLRsWritten-eng.EndsWritten:] {
		fmt.Println(" ", r)
	}
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func trimZeros(b []byte) []byte {
	n := 0
	for n < len(b) && b[n] != 0 {
		n++
	}
	return b[:n]
}

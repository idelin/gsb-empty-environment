// Command aries-demo builds a WAL with five transactions, simulates a crash
// after a checkpoint, then runs the ARIES three-phase recovery and prints
// what each phase did.
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"aries/recovery"
	"aries/wal"
)

func main() {
	dir, err := os.MkdirTemp("", "aries-demo-*")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)
	logPath := filepath.Join(dir, "wal.log")

	// --- Pre-crash: generate the workload ---------------------------------
	w, err := wal.NewWriter(logPath)
	if err != nil {
		log.Fatal(err)
	}
	sc, err := recovery.BuildCrashScenario(w)
	if err != nil {
		log.Fatal(err)
	}
	endOfLog := w.NextLSN()
	w.Close()
	fmt.Printf("pre-crash: %d log records, winners=%v losers=%v, log bytes=%d\n",
		sc.RecordCnt, sc.Winners, sc.Losers, endOfLog)
	fmt.Println("*** CRASH *** (no END records written for open transactions)")

	// --- Recovery -----------------------------------------------------------
	r, err := wal.Open(logPath)
	if err != nil {
		log.Fatal(err)
	}
	defer r.Close()

	store := recovery.NewStorage()
	for _, p := range sc.PageIDs {
		store.SeedCleanPage(p, make([]byte, 16)) // stale zeroed pages on disk
	}

	aw, err := wal.AppendWriter(logPath, endOfLog)
	if err != nil {
		log.Fatal(err)
	}
	rep, err := recovery.NewEngine(store).Recover(r, aw)
	if err != nil {
		log.Fatal(err)
	}
	aw.Close()

	fmt.Println("\n[analysis] transaction table:")
	for _, id := range []uint64{1, 2, 3, 4, 5} {
		if te, ok := rep.Txns[id]; ok {
			fmt.Printf("  txn=%d lastLSN=%d status=%d\n", te.TxnID, te.LastLSN, te.Status)
		}
	}
	fmt.Println("[analysis] dirty page table:")
	for _, p := range sc.PageIDs {
		fmt.Printf("  page=%d recLSN=%d\n", p, rep.DirtyPages[p])
	}
	fmt.Printf("\n[redo] startLSN=%d re-applied=%d skipped=%d\n", rep.RedoStart, rep.Redone, rep.RedoSkip)
	fmt.Printf("[undo] losers=%v CLRs written=%d ENDs written=%d\n", rep.UndoneTxns, rep.CLRs, rep.Ends)

	fmt.Println("\n[result] durable pages after recovery:")
	ok := true
	for _, p := range sc.PageIDs {
		pg, _ := store.ReadPage(p)
		match := "OK"
		if string(pg.Data[:len(sc.Expect[p])]) != string(sc.Expect[p]) {
			match, ok = "MISMATCH", false
		}
		fmt.Printf("  page=%d pageLSN=%d data=%q %s\n", p, pg.LSN, pg.Data[:12], match)
	}
	if !ok {
		log.Fatal("recovery verification failed")
	}
	fmt.Println("\nrecovery verified: committed data persisted, uncommitted data rolled back")
}

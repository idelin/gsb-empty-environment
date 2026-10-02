// Command ariesdemo builds a WAL with 2 committed and 3 uncommitted
// transactions, crashes after a checkpoint, then runs the ARIES
// three-phase recovery and prints every step.
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"aries"
)

func main() {
	dir, err := os.MkdirTemp("", "aries-demo-*")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)
	walPath := filepath.Join(dir, "demo.wal")

	fmt.Println("=== Phase 0: run workload, then crash ===")
	sc, err := aries.BuildCrashScenario(walPath)
	if err != nil {
		log.Fatal(err)
	}
	sc.WAL.Close()
	// Reopen just for reading the pre-crash log.
	wal0, _ := aries.OpenWAL(walPath)
	recs, _ := wal0.Scan()
	wal0.Close()
	for _, r := range recs {
		fmt.Printf("  WAL  %s\n", r)
	}
	fmt.Println("  *** CRASH: buffer pool lost ***")
	fmt.Println("  Disk at crash time:")
	for _, id := range sc.PageNames {
		p, _ := sc.Disk.ReadPage(id)
		fmt.Printf("    P%d pageLSN=%d data=%q\n", id, p.PageLSN, p.Data)
	}

	fmt.Println("\n=== ARIES recovery ===")
	wal, err := aries.OpenWAL(walPath)
	if err != nil {
		log.Fatal(err)
	}
	defer wal.Close()
	res, err := aries.NewEngine(wal, sc.Disk).Recover()
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("-- Analysis --")
	fmt.Printf("  redo starts at LSN %d\n", res.RedoLSN)
	for id, t := range res.TxnTable {
		fmt.Printf("  txn %d status=%s lastLSN=%d\n", id, t.Status, t.LastLSN)
	}
	for page, recLSN := range res.DirtyPage {
		fmt.Printf("  dirty page P%d recLSN=%d\n", page, recLSN)
	}

	fmt.Println("-- Redo (repeat history) --")
	fmt.Printf("  re-applied LSNs: %v\n", res.Redone)

	fmt.Println("-- Undo (losers, reverse order, CLRs) --")
	for _, clr := range res.CLRs {
		fmt.Printf("  %s\n", clr)
	}
	fmt.Printf("  END written for txns: %v\n", res.EndedTxns)

	fmt.Println("\n=== Final durable state ===")
	ok := true
	for _, id := range sc.PageNames {
		p, _ := sc.Disk.ReadPage(id)
		want := sc.Expected[id]
		status := "OK"
		if string(p.Data) != want {
			status, ok = "MISMATCH", false
		}
		fmt.Printf("  P%d pageLSN=%d data=%q (expected %q) %s\n",
			id, p.PageLSN, p.Data, want, status)
	}
	if !ok {
		os.Exit(1)
	}
	fmt.Println("\nRecovery verified: committed data persisted, uncommitted rolled back.")
}

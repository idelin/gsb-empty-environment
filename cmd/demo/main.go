// Command demo runs the full ARIES crash-recovery scenario end to end:
// 2 committed + 3 uncommitted transactions, a checkpoint, a sudden power
// loss, then the three recovery phases (Analysis, Redo, Undo).
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"aries/recovery"
	"aries/storage"
	"aries/wal"
)

func main() {
	dir, err := os.MkdirTemp("", "aries-demo")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "wal.log")

	w, err := wal.Open(path, nil)
	if err != nil {
		log.Fatal(err)
	}
	store := storage.NewPageStore()
	tm := recovery.NewTxnManager(w, store)

	fmt.Println("=== Phase 0: run transactions ===")
	txn1 := tm.Begin()
	must(tm.Update(txn1, 1, 0, []byte("AAAA")))
	must(tm.Commit(txn1))
	fmt.Println("txn1: UPDATE page1 <- AAAA, COMMIT")

	cpLSN, err := tm.Checkpoint()
	must(err)
	fmt.Printf("checkpoint written at LSN %d (buffer pool + log flushed)\n", cpLSN)

	txn2 := tm.Begin()
	must(tm.Update(txn2, 2, 0, []byte("BBBB")))
	must(tm.Commit(txn2))
	fmt.Println("txn2: UPDATE page2 <- BBBB, COMMIT (dirty page only in buffer pool)")

	for i, page := range []uint32{3, 4, 5} {
		txn := tm.Begin()
		data := []byte{byte('C' + i), byte('C' + i), byte('C' + i), byte('C' + i)}
		must(tm.Update(txn, page, 0, data))
		fmt.Printf("txn%d: UPDATE page%d <- %s, never commits\n", txn, page, data)
	}

	must(w.Flush())
	fmt.Println("\n=== CRASH: sudden power loss (buffer pool vaporized) ===")
	store.Crash()
	w.Close()

	w2, err := wal.Open(path, nil)
	if err != nil {
		log.Fatal(err)
	}
	rm := &recovery.RecoveryManager{Wal: w2, Store: store}
	rep, err := rm.Recover()
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("\n=== Phase 1: Analysis ===")
	fmt.Printf("checkpoint at LSN %d; dirty page table: %v\n",
		rep.Analysis.CheckpointLSN, rep.Analysis.DPT)
	fmt.Printf("loser transactions (uncommitted at crash): %v\n", rep.Losers)

	fmt.Println("\n=== Phase 2: Redo (repeat history) ===")
	fmt.Printf("re-applied %d records, skipped %d already-durable records\n",
		rep.RedoApplied, rep.RedoSkipped)

	fmt.Println("\n=== Phase 3: Undo (rollback losers with CLRs) ===")
	fmt.Printf("wrote %d compensation log records, %d END records\n",
		rep.UndoCLRs, rep.EndsWritten)

	fmt.Println("\n=== Final state ===")
	for page := uint32(1); page <= 5; page++ {
		fmt.Printf("page%d: %q\n", page, store.Fetch(page).Data[:4])
	}
	w2.Close()
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

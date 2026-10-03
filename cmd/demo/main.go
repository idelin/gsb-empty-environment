// Command demo builds a WAL with 5 transactions (2 committed, 3 uncommitted),
// crashes after a checkpoint, and runs the ARIES three-phase recovery.
package main

import (
	"fmt"
	"os"

	"aries"
)

func main() {
	dir, err := os.MkdirTemp("", "aries-demo-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	fmt.Println("=== Phase 0: build workload, checkpoint, then crash ===")
	sc, err := aries.BuildCrashScenario(dir)
	if err != nil {
		panic(err)
	}
	fmt.Printf("committed txns:   %v\n", sc.CommittedTxns)
	fmt.Printf("uncommitted txns: %v (t5 aborting)\n", sc.UncommittedTxns)
	fmt.Printf("checkpoint LSN:   %d\n", sc.CheckpointLSN)

	wal, err := aries.OpenWAL(sc.WALPath)
	if err != nil {
		panic(err)
	}
	records, err := wal.Records()
	if err != nil {
		panic(err)
	}
	wal.Close()
	fmt.Println("\nWAL contents at crash time:")
	for _, r := range records {
		switch r.Type {
		case aries.LogUpdate, aries.LogCLR:
			fmt.Printf("  LSN=%-4d txn=%d %-6s page=%d off=%d old=%d new=%d prev=%d undoNext=%d\n",
				r.LSN, r.TxnID, r.Type, r.PageID, r.Offset, r.OldValue, r.NewValue, r.PrevLSN, r.UndoNext)
		case aries.LogCheckpoint:
			fmt.Printf("  LSN=%-4d txn=%d %-6s DPT=%v TT=%v\n", r.LSN, r.TxnID, r.Type, r.Checkpoint.DPT, r.Checkpoint.TT)
		default:
			fmt.Printf("  LSN=%-4d txn=%d %-6s prev=%d\n", r.LSN, r.TxnID, r.Type, r.PrevLSN)
		}
	}

	fmt.Println("\n=== ARIES recovery: analysis -> redo -> undo ===")
	report, err := aries.Recover(sc.WALPath, sc.PagePath)
	if err != nil {
		panic(err)
	}
	fmt.Printf("analysis: checkpointLSN=%d redoStartLSN=%d DPT=%v\n",
		report.CheckpointLSN, report.RedoStartLSN, report.DPT)
	fmt.Printf("analysis: losers=%v winners=%v\n", report.Losers, report.Winners)
	fmt.Printf("redo:     %d records re-applied (history repeated)\n", report.Redone)
	fmt.Printf("undo:     %d updates rolled back, %d CLRs written\n", report.Undone, report.CLRsWritten)

	fmt.Println("\n=== Verification of final page contents ===")
	store, err := aries.OpenPageStore(sc.PagePath)
	if err != nil {
		panic(err)
	}
	defer store.Close()
	ok := true
	for _, want := range aries.ExpectedCells {
		page, err := store.ReadPage(want.PageID)
		if err != nil {
			panic(err)
		}
		got := page.Get(want.Off)
		status := "PASS"
		if got != want.Val {
			status = "FAIL"
			ok = false
		}
		fmt.Printf("  page=%d off=%-3d expect=%-4d got=%-4d %s\n", want.PageID, want.Off, want.Val, got, status)
	}
	if !ok {
		fmt.Println("\nRESULT: RECOVERY INCORRECT")
		os.Exit(1)
	}
	fmt.Println("\nRESULT: committed data persisted, uncommitted data rolled back. OK")
}

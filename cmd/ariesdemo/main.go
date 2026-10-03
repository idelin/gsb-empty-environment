// Command ariesdemo builds a WAL containing 2 committed and 3
// in-flight transactions, crashes after a checkpoint, then runs the
// ARIES three-phase restart recovery and the invariant inspector.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"aries/inspect"
	"aries/recovery"
	"aries/storage"
	"aries/wal"
)

func main() {
	dir, err := os.MkdirTemp("", "aries-demo-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	logPath := filepath.Join(dir, "wal.log")
	diskPath := filepath.Join(dir, "pages.db")
	masterPath := filepath.Join(dir, "master")

	fmt.Println("=== Phase 1: normal processing (5 txns, checkpoint, then crash) ===")
	disk, err := storage.OpenDiskStore(diskPath)
	must(err)
	log, err := wal.OpenLog(logPath)
	must(err)
	pool := storage.NewBufferPool(disk)
	sys := recovery.NewSystem(log, pool)

	exp, chkLSN, err := recovery.BuildCrashScenario(sys, disk)
	must(err)
	must(wal.WriteMaster(masterPath, chkLSN))
	log.ScanFrom(0, func(rec *wal.Record) error {
		fmt.Println("  ", rec)
		return nil
	})
	fmt.Printf("checkpoint at LSN %d; log flushed through LSN %d\n", chkLSN, log.FlushedLSN())
	fmt.Println("pages durable at crash time:")
	for page := 1; page <= 5; page++ {
		fmt.Printf("  P%d = %q (pageLSN %d)\n", page, mustContents(disk, page), disk.PageLSN(page))
	}

	fmt.Println("\n=== Phase 2: crash! buffer pool discarded, log + disk survive ===")
	pool = nil
	log.Close()
	log = nil

	fmt.Println("\n=== Phase 3: ARIES restart recovery ===")
	log, err = wal.OpenLog(logPath)
	must(err)
	pool = storage.NewBufferPool(disk)
	engine := &recovery.Engine{Log: log, Pool: pool, Disk: disk}
	masterLSN, err := wal.ReadMaster(masterPath)
	must(err)
	rep, err := engine.Restart(masterLSN)
	must(err)

	fmt.Println("-- analysis: transaction table")
	txns := make([]int, 0, len(rep.Analysis.ATT))
	for txn := range rep.Analysis.ATT {
		txns = append(txns, txn)
	}
	sort.Ints(txns)
	for _, txn := range txns {
		e := rep.Analysis.ATT[txn]
		fmt.Printf("  txn %d status=%s lastLSN=%d\n", txn, e.Status, e.LastLSN)
	}
	fmt.Println("-- analysis: dirty page table")
	pages := make([]int, 0, len(rep.Analysis.DPT))
	for page := range rep.Analysis.DPT {
		pages = append(pages, page)
	}
	sort.Ints(pages)
	for _, page := range pages {
		fmt.Printf("  page %d recLSN=%d\n", page, rep.Analysis.DPT[page])
	}
	fmt.Printf("-- redo: repeated %d log record(s) (history repeated)\n", rep.Redone)
	fmt.Printf("-- undo: losers=%v winners=%v, wrote %d CLR(s) and %d END record(s)\n",
		rep.Losers, rep.Winners, len(rep.CLRs), len(rep.Ends))

	fmt.Println("\n=== Phase 4: invariant inspection ===")
	inspector := inspect.NewInspector(log, pool, disk)
	violations, err := inspector.CheckAll(rep.Analysis)
	must(err)
	if len(violations) == 0 {
		fmt.Println("  all invariants hold: no dangling references, no orphan loops, no skew")
	} else {
		for _, v := range violations {
			fmt.Println("  VIOLATION:", v)
		}
		os.Exit(1)
	}

	fmt.Println("\n=== Phase 5: final durable state vs expectation ===")
	if err := recovery.VerifyExpectation(disk, exp); err != nil {
		fmt.Println("  MISMATCH:", err)
		os.Exit(1)
	}
	for page := 1; page <= 5; page++ {
		fmt.Printf("  P%d = %q (expected %q)\n", page, mustContents(disk, page), exp.Pages[page])
	}
	fmt.Println("\nrecovery verified: committed txns", exp.Winners, "persisted; losers", exp.Losers, "rolled back")
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func mustContents(disk *storage.DiskStore, page int) string {
	s, err := recovery.PageContents(disk, page)
	if err != nil {
		panic(err)
	}
	return s
}

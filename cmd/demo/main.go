// Command demo builds the acceptance scenario end to end: five
// transactions (two committed, three uncommitted), a fuzzy checkpoint,
// a crash, and ARIES three-phase recovery — printing every step.
package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"

	"aries/aries"
)

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func write(tm *aries.TxnMgr, txn uint64, page uint32, val string) {
	old, err := tm.ReadAt(page, 0, len(val))
	must(err)
	_, err = tm.Write(txn, page, 0, old, []byte(val))
	must(err)
	fmt.Printf("  txn=%d WRITE page=%d val=%q\n", txn, page, val)
}

func dumpPages(store *aries.PageStore, pages int, label string) {
	fmt.Printf("--- %s ---\n", label)
	for i := 0; i < pages; i++ {
		lsn, data, err := store.Read(uint32(i))
		must(err)
		fmt.Printf("  page %d: pageLSN=%-3d data=%q\n", i, lsn, string(data[:8]))
	}
}

func main() {
	dir, err := ioutil.TempDir("", "aries-demo-")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)
	walPath := filepath.Join(dir, "wal.log")
	pagePath := filepath.Join(dir, "pages.db")

	wal, err := aries.OpenWAL(walPath)
	must(err)
	store, err := aries.OpenPageStore(pagePath, 64)
	must(err)
	pool := aries.NewBufferPool(store)
	tm := aries.NewTxnMgr(wal, pool)

	fmt.Println("== Phase 1: run transactions (before checkpoint) ==")
	t1 := tm.Begin()
	write(tm, t1, 0, "AAAA0001")
	write(tm, t1, 1, "BBBB0001")
	must(tm.Commit(t1))
	fmt.Println("  txn=1 COMMIT")
	must(pool.FlushAll()) // background page flush

	t2 := tm.Begin()
	write(tm, t2, 2, "CCCC0002")
	t3 := tm.Begin()
	write(tm, t3, 0, "aaaat3a!")
	t4 := tm.Begin()
	write(tm, t4, 3, "ddddt4a!")
	t5 := tm.Begin()
	write(tm, t5, 1, "bbbbt5a!")

	fmt.Println("== Fuzzy checkpoint ==")
	chk, err := tm.Checkpoint()
	must(err)
	fmt.Printf("  END_CHKPT at LSN %d\n", chk)

	fmt.Println("== Phase 2: more work (after checkpoint) ==")
	write(tm, t2, 2, "CCCC-002")
	must(tm.Commit(t2))
	fmt.Println("  txn=2 COMMIT")
	write(tm, t3, 0, "aaaat3b!")
	write(tm, t4, 3, "ddddt4b!")
	must(tm.StartAbort(t4))
	more, err := tm.RollbackStep(t4) // crash mid-abort: one CLR written
	must(err)
	fmt.Printf("  txn=4 ABORT, one CLR written (more undo pending: %v)\n", more)
	write(tm, t5, 1, "bbbbt5b!")

	fmt.Println("== CRASH: log durable, buffer pool lost ==")
	must(wal.Flush())
	wal.Close()
	store.Close()

	fmt.Println("== Reboot: ARIES three-phase recovery ==")
	wal2, err := aries.OpenWAL(walPath)
	must(err)
	store2, err := aries.OpenPageStore(pagePath, 64)
	must(err)
	pool2 := aries.NewBufferPool(store2)

	stats, err := aries.Recover(wal2, pool2)
	must(err)
	fmt.Printf("  analysis: records=%d checkpointLSN=%d\n", stats.AnalyzedRecords, stats.CheckpointLSN)
	fmt.Printf("  redo:     fromLSN=%d redone=%d skipped=%d\n", stats.RedoFromLSN, stats.RedoneOps, stats.SkippedOps)
	fmt.Printf("  undo:     losers=%v CLRs=%d\n", stats.Losers, stats.UndoneOps)
	fmt.Printf("  winners (committed): %v\n", stats.Winners)

	dumpPages(store2, 4, "Recovered page file (t1/t2 committed data kept, t3/t4/t5 rolled back)")

	fmt.Println("== WAL after recovery (tail) ==")
	count := 0
	must(wal2.Scan(func(r *aries.LogRecord) bool { count++; return true }))
	shown := 0
	must(wal2.Scan(func(r *aries.LogRecord) bool {
		if shown >= count-8 {
			fmt.Printf("  %s\n", r)
		}
		shown++
		return true
	}))
	wal2.Close()
	store2.Close()
}

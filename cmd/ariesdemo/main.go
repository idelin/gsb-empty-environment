// Command ariesdemo builds a WAL with committed and uncommitted
// transactions, simulates a crash after a checkpoint, then runs the
// ARIES three-phase recovery and prints every step.
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
	dir, err := os.MkdirTemp("", "ariesdemo-*")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "demo.wal")

	fmt.Println("=== Phase 0: normal processing, building the WAL ===")
	w, err := wal.Open(path, 64)
	if err != nil {
		log.Fatal(err)
	}
	lsn := map[string]wal.LSN{}
	put := func(key string, r *wal.Record) {
		lsn[key] = w.Append(r)
		fmt.Printf("  LSN %-3d %-10s txn=%d page=%d %q -> %q\n",
			r.LSN, r.Type, r.TxnID, r.PageID, r.Before, r.After)
	}
	put("t1p1", &wal.Record{TxnID: 1, Type: wal.TypeUpdate, PageID: 1, Before: []byte("init-P1"), After: []byte("T1-P1")})
	put("t1p2", &wal.Record{TxnID: 1, Type: wal.TypeUpdate, PageID: 2, PrevLSN: lsn["t1p1"], Before: []byte("init-P2"), After: []byte("T1-P2")})
	put("t2p3", &wal.Record{TxnID: 2, Type: wal.TypeUpdate, PageID: 3, Before: []byte("init-P3"), After: []byte("T2-P3")})
	w.Append(&wal.Record{Type: wal.TypeCheckpoint,
		ChkTT: wal.TxnTable{
			1: {Status: wal.StatusRunning, LastLSN: lsn["t1p2"]},
			2: {Status: wal.StatusRunning, LastLSN: lsn["t2p3"]},
		},
		ChkDPT: wal.DirtyPageTable{1: lsn["t1p1"], 2: lsn["t1p2"], 3: lsn["t2p3"]},
	})
	fmt.Println("  --- CHECKPOINT (T1, T2 in flight; P1..P3 dirty) ---")
	put("t1c", &wal.Record{TxnID: 1, Type: wal.TypeCommit, PrevLSN: lsn["t1p2"]})
	put("t1e", &wal.Record{TxnID: 1, Type: wal.TypeEnd, PrevLSN: lsn["t1c"]})
	put("t2c", &wal.Record{TxnID: 2, Type: wal.TypeCommit, PrevLSN: lsn["t2p3"]})
	put("t2e", &wal.Record{TxnID: 2, Type: wal.TypeEnd, PrevLSN: lsn["t2c"]})
	put("t3p1", &wal.Record{TxnID: 3, Type: wal.TypeUpdate, PageID: 1, Before: []byte("T1-P1"), After: []byte("T3-P1")})
	put("t3p4", &wal.Record{TxnID: 3, Type: wal.TypeUpdate, PageID: 4, PrevLSN: lsn["t3p1"], Before: []byte("init-P4"), After: []byte("T3-P4")})
	put("t4p2", &wal.Record{TxnID: 4, Type: wal.TypeUpdate, PageID: 2, Before: []byte("T1-P2"), After: []byte("T4-P2")})
	put("t4p5", &wal.Record{TxnID: 4, Type: wal.TypeUpdate, PageID: 5, PrevLSN: lsn["t4p2"], Before: []byte("init-P5"), After: []byte("T4-P5")})
	put("t5p3", &wal.Record{TxnID: 5, Type: wal.TypeUpdate, PageID: 3, Before: []byte("T2-P3"), After: []byte("T5-P3")})
	if err := w.Close(); err != nil {
		log.Fatal(err)
	}
	fmt.Println("  *** CRASH *** (T3, T4, T5 never committed)")

	// Disk at crash time: only P1 was flushed (with T1's update).
	store := recovery.NewPageStore()
	for pid := wal.PageID(1); pid <= 5; pid++ {
		store.Put(pid, &recovery.Page{Data: []byte(fmt.Sprintf("init-P%d", pid))})
	}
	store.Put(1, &recovery.Page{PageLSN: lsn["t1p1"], Data: []byte("T1-P1")})

	fmt.Println("\n=== ARIES recovery ===")
	eng := recovery.NewEngine(path, store)
	ar, stats, err := eng.Recover()
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("[analysis] loser transactions:", recovery.SortedTxnIDs(ar.TT))
	fmt.Print("[analysis] dirty page table: {")
	for pid := wal.PageID(1); pid <= 5; pid++ {
		fmt.Printf("P%d@LSN%d ", pid, ar.DPT[pid])
	}
	fmt.Println("}")
	fmt.Printf("[redo]      repeated %d updates starting at LSN %d\n", stats.RedoneUpdates, ar.RedoFrom)
	fmt.Printf("[undo]      rolled back %d updates, wrote %d CLRs and %d ENDs\n",
		stats.UndoneUpdates, stats.CLRsWritten, stats.EndsWritten)

	fmt.Println("\n=== Final page contents (committed data durable, losers rolled back) ===")
	for pid := wal.PageID(1); pid <= 5; pid++ {
		p := store.Get(pid)
		fmt.Printf("  P%d = %-8q (pageLSN %d)\n", pid, p.Data, p.PageLSN)
	}

	fmt.Println("\n=== WAL after recovery (compensation records appended) ===")
	final, err := wal.Scan(path)
	if err != nil {
		log.Fatal(err)
	}
	for _, r := range final {
		if r.Type == wal.TypeCLR || (r.Type == wal.TypeEnd && r.TxnID >= 3) {
			fmt.Printf("  LSN %-3d %-4s txn=%d page=%d undoNext=%d restores %q\n",
				r.LSN, r.Type, r.TxnID, r.PageID, r.UndoNext, r.After)
		}
	}
}

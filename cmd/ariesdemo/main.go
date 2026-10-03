// Command ariesdemo builds the acceptance scenario end to end:
// a WAL with 3 uncommitted + 2 committed transactions, a checkpoint,
// a crash, then ARIES analysis/redo/undo recovery.
package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"aries/db"
	"aries/recovery"
	"aries/storage"
	"aries/wal"
)

func pageVal(b byte) []byte { return bytes.Repeat([]byte{b}, storage.PageSize) }

func main() {
	dir, err := os.MkdirTemp("", "ariesdemo")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	w, err := wal.Open(filepath.Join(dir, "demo.wal"))
	if err != nil {
		panic(err)
	}
	defer w.Close()
	disk := storage.NewDisk()
	database := db.New(w, disk)

	fmt.Println("=== 1. Run workload: 5 transactions (3 will not commit) ===")
	database.Update(1, 10, pageVal(0xA1))
	database.Update(1, 11, pageVal(0xA1))
	database.Update(2, 11, pageVal(0xB2))
	database.Update(4, 12, pageVal(0xD4))
	database.Commit(4)
	database.Update(5, 13, pageVal(0xE5))
	database.Commit(5)
	ckpt := database.Checkpoint()
	database.Update(3, 10, pageVal(0xC3))
	database.Update(3, 13, pageVal(0xC3))
	fmt.Printf("checkpoint written at LSN %d\n", ckpt)

	fmt.Println("=== 2. Steal dirty pages 10,12 to disk; flush nothing else ===")
	database.Flush(10)
	database.Flush(12)

	fmt.Println("=== 3. CRASH: buffer pool and transaction table lost ===")
	database.Crash()

	fmt.Println("\nWAL at crash time:")
	for _, r := range w.Records() {
		fmt.Println("  ", r)
	}

	fmt.Println("\n=== 4. ARIES recovery ===")
	st := recovery.NewEngine(w, disk).Recover()
	fmt.Printf("analysis: checkpoint=%d txns=%d redoStart=%d\n",
		st.CheckpointLSN, st.TxnsAnalyzed, st.RedoStartLSN)
	fmt.Printf("redo:     %d records reapplied (repeat history)\n", st.Redone)
	fmt.Printf("undo:     losers=%v, %d updates rolled back via %d CLRs\n",
		st.Losers, st.Undone, st.CLRsWritten)

	fmt.Println("\nRecovered disk state:")
	for id, p := range disk.Snapshot() {
		fmt.Printf("   page %2d = 0x%02x... (pageLSN %d)\n", id, p.Data[0], p.LSN)
	}

	fmt.Println("\nWAL records appended by recovery:")
	for _, r := range w.Records() {
		if r.LSN > w.LastLSN()-wal.LSN(st.CLRsWritten+len(st.Losers)) {
			fmt.Println("  ", r)
		}
	}

	// Verify the acceptance criteria.
	check := func(page uint32, want byte) {
		p, _ := disk.Read(page)
		if p.Data[0] != want {
			fmt.Printf("FAIL: page %d = 0x%02x, want 0x%02x\n", page, p.Data[0], want)
			os.Exit(1)
		}
	}
	check(12, 0xD4) // committed txn 4 persisted
	check(13, 0xE5) // committed txn 5 persisted (txn 3's write undone)
	check(10, 0x00) // uncommitted txns 1,3 rolled back
	check(11, 0x00) // uncommitted txns 1,2 rolled back
	fmt.Println("\nOK: committed data persisted, uncommitted data rolled back.")
}

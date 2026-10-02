// Command demo builds a WAL with 5 transactions (2 committed, 3 uncommitted),
// checkpoints, crashes, and runs the ARIES three-phase restart recovery.
// It also exercises the adaptive sparse vector optimizer.
package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"aries/recovery"
	"aries/sparse"
	"aries/wal"
)

func main() {
	dir, err := ioutil.TempDir("", "aries-demo")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "demo.log")

	log, _ := wal.Open(path)
	disk := recovery.NewMemDisk()
	bp := recovery.NewBufferPool(disk, 64)
	tm := recovery.NewTxnManager(log, bp)

	put := func(s string) []byte { return []byte(s) }

	fmt.Println("=== Phase 0: normal processing ===")
	t1 := tm.Begin()
	tm.Update(t1, 1, 0, put("AAAA"))
	tm.Update(t1, 2, 0, put("1111"))
	t2 := tm.Begin()
	tm.Update(t2, 1, 8, put("BBBB"))
	bp.FlushPage(1) // dirty page (incl. uncommitted BBBB) reaches disk
	tm.Checkpoint()
	fmt.Println("-- checkpoint written --")
	tm.Update(t1, 3, 0, put("cccc"))
	tm.Commit(t1)
	t3 := tm.Begin()
	tm.Update(t3, 2, 8, put("3333"))
	tm.Commit(t3)
	t4 := tm.Begin()
	tm.Update(t4, 1, 16, put("DDDD"))
	t5 := tm.Begin()
	tm.Update(t5, 2, 16, put("5555"))
	tm.Abort(t5)
	tm.Update(t2, 3, 8, put("2222"))
	fmt.Println("T1,T3 committed; T2,T4 running; T5 aborting")

	fmt.Println("\n=== CRASH: buffer pool lost, log + disk survive ===")
	bp.Crash()
	log.Close()

	log2, _ := wal.Open(path)
	res, err := recovery.NewEngine(log2, recovery.NewBufferPool(disk, 64)).Recover()
	if err != nil {
		panic(err)
	}

	fmt.Println("\n=== Phase 1: Analysis ===")
	fmt.Printf("TransactionTable: %d entries\n", len(res.TxTable))
	for id, e := range res.TxTable {
		fmt.Printf("  T%d status=%s lastLSN=%d\n", id, e.Status, e.LastLSN)
	}
	fmt.Printf("DirtyPageTable: %v\n", res.DirtyPages)

	fmt.Println("\n=== Phase 2: Redo (repeat history) ===")
	fmt.Printf("re-applied %d records: %v\n", len(res.Redone), res.Redone)

	fmt.Println("\n=== Phase 3: Undo (losers, CLRs) ===")
	fmt.Printf("winners=%v losers=%v\n", res.Winners, res.Losers)
	fmt.Printf("undone %d updates, wrote %d CLRs: %v\n", len(res.Undone), len(res.CLRsWritten), res.CLRsWritten)

	fmt.Println("\n=== Final durable state ===")
	for _, id := range []wal.PageID{1, 2, 3} {
		p, _ := disk.ReadPage(id)
		fmt.Printf("page %d: %q\n", id, p.Data[:20])
	}

	fmt.Println("\n=== Sparse optimizer demo ===")
	v := sparse.New()
	for i := 0; i < 100000; i++ {
		v.Set(uint32(i)*100000, int64(i+1)) // 1e5 entries over a 1e10 span
	}
	fmt.Printf("1e5 entries over 1e10 span: dense=%v mem~%dKB (dense array would need ~80GB)\n",
		v.IsDense(), v.MemEstimate()/1024)
	d := sparse.New()
	for i := 0; i < 50000; i++ {
		d.Set(uint32(i), int64(i+1))
	}
	fmt.Printf("5e4 contiguous entries: dense=%v mem~%dKB\n", d.IsDense(), d.MemEstimate()/1024)
	comp := d.Compress()
	fmt.Printf("compressed snapshot: %d bytes (%.1fx)\n", len(comp), float64(d.MemEstimate())/float64(len(comp)))
	back, _ := sparse.Decompress(comp)
	fmt.Printf("decompressed entries: %d (match=%v)\n", back.Len(), back.Get(12345) == d.Get(12345))
	log2.Close()
}

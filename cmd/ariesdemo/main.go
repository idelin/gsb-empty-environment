// Command ariesdemo builds a WAL containing 2 committed and 3
// uncommitted transactions with a checkpoint, simulates a crash, runs
// the ARIES three-phase recovery and prints the resulting state plus
// the fine-grained metrics export.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"aries/buffer"
	"aries/metrics"
	"aries/recovery"
	"aries/wal"
)

func main() {
	dir, err := os.MkdirTemp("", "ariesdemo")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "wal.log")

	reg := metrics.NewRegistry()
	pool := buffer.NewPool(64, reg.Counter("buffer.hits"), reg.Counter("buffer.misses"))

	// --- Pre-crash: run a workload that emits WAL records. ---
	w, err := wal.NewWriter(path)
	must(err)
	b := wal.NewBuilder(w)
	t1 := b.Update(1, 1, []byte("A"), []byte("A1"))
	b.Commit(1)
	b.End(1)
	b.Update(2, 2, []byte("B"), []byte("B1"))
	b.Checkpoint()
	b.Update(3, 1, []byte("A1"), []byte("A2"))
	b.Update(2, 3, []byte("C"), []byte("C1"))
	b.Commit(2)
	b.Update(4, 4, []byte("D"), []byte("D1"))
	b.Update(5, 3, []byte("C1"), []byte("C2"))
	must(w.Close())

	// P1 was flushed to disk right after T1's update; then crash.
	pg := pool.Fetch(1)
	pg.Data, pg.LSN, pg.Dirty = []byte("A1"), t1, true
	pool.FlushPage(1)
	pool.Crash()
	fmt.Println("== crash simulated: T1,T2 committed; T3,T4,T5 in flight ==")

	// --- Recovery. ---
	eng := recovery.NewEngine(path, pool, reg)
	rep, err := eng.Recover()
	must(err)
	fmt.Printf("losers=%v winners=%v redone=%d undone=%d clrs=%d\n\n",
		rep.Losers, rep.Winners, rep.Redone, rep.Undone, rep.CLRs)

	fmt.Println("== durable pages after recovery ==")
	ids := []uint64{1, 2, 3, 4}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		p, _ := pool.DiskPage(id)
		fmt.Printf("page %d: data=%q pageLSN=%d\n", id, p.Data, p.LSN)
	}

	fmt.Printf("\nbuffer hit rate: %.2f\n\n", pool.HitRate())
	fmt.Println("== metrics (text export) ==")
	must(reg.ExportText(os.Stdout))
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

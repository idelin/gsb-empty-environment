// Command ariesdemo builds a WAL with committed and uncommitted
// transactions, crashes after a checkpoint, runs ARIES three-phase recovery,
// and then stresses the multi-node conflict arbiter.
package main

import (
	"encoding/binary"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"aries"
)

func u64(v uint64) []byte {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], v)
	return b[:]
}

func pageVal(d *aries.Disk, id int64) uint64 {
	return binary.LittleEndian.Uint64(d.ReadPage(id).Data[0:8])
}

func main() {
	dir, err := os.MkdirTemp("", "ariesdemo")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)
	walPath := filepath.Join(dir, "demo.wal")

	fmt.Println("=== Phase 0: build workload, then crash after checkpoint ===")
	wal, err := aries.CreateWAL(walPath)
	if err != nil {
		log.Fatal(err)
	}
	disk := aries.NewDisk()
	db := aries.NewDB(wal, disk, nil)

	t1 := db.Begin()
	must(db.Write(t1, 1, 0, u64(100)))
	must(db.Write(t1, 2, 0, u64(200)))
	db.Commit(t1)
	t3 := db.Begin()
	must(db.Write(t3, 1, 0, u64(111)))
	db.Checkpoint()
	t2 := db.Begin()
	must(db.Write(t2, 2, 0, u64(300)))
	db.Commit(t2)
	t4 := db.Begin()
	must(db.Write(t4, 3, 0, u64(333)))
	t5 := db.Begin()
	must(db.Write(t5, 2, 0, u64(222)))
	must(db.Write(t5, 3, 0, u64(555)))
	wal.Close()
	fmt.Println("txns: T1,T2 committed; T3,T4,T5 uncommitted; CRASH (buffer pool lost)")
	fmt.Printf("disk at crash: P1=%d P2=%d P3=%d (P1 holds uncommitted 111!)\n\n",
		pageVal(disk, 1), pageVal(disk, 2), pageVal(disk, 3))

	fmt.Println("=== WAL image at crash ===")
	printWAL(walPath)

	fmt.Println("\n=== Recovery: Analysis -> Redo -> Undo ===")
	rwal, err := aries.OpenWAL(walPath)
	if err != nil {
		log.Fatal(err)
	}
	rep, err := aries.NewRecoveryEngine(rwal, disk).Recover()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("analysis: checkpoint=%v DPT=%v committed=%v losers=%v\n",
		rep.CheckpointFound, rep.DPT, rep.Committed, rep.Losers)
	fmt.Printf("redo:     re-applied %d records (LSN %v), skipped %d already-durable\n",
		len(rep.Redone), rep.Redone, len(rep.RedoSkipped))
	fmt.Printf("undo:     rolled back LSN %v via %d CLRs\n", rep.Undone, len(rep.CLRs))
	rwal.Close()

	fmt.Printf("\ndisk after recovery: P1=%d P2=%d P3=%d\n",
		pageVal(disk, 1), pageVal(disk, 2), pageVal(disk, 3))
	fmt.Println("expected:            P1=100 P2=300 P3=0 (committed kept, losers undone)")

	fmt.Println("\n=== WAL after recovery (CLRs + END appended) ===")
	printWAL(walPath)

	fmt.Println("\n=== Multi-node conflict arbiter stress ===")
	stressArbiter()
}

func printWAL(path string) {
	wal, err := aries.OpenWAL(path)
	if err != nil {
		log.Fatal(err)
	}
	defer wal.Close()
	records, err := wal.ReadAll()
	if err != nil {
		log.Fatal(err)
	}
	for _, r := range records {
		fmt.Printf("  %s\n", r)
	}
}

func stressArbiter() {
	const nodes, txnsPerNode, accounts = 8, 500, 16
	lm := aries.NewLockManager()
	balances := make([]int64, accounts)
	for i := range balances {
		balances[i] = 1000
	}
	var seq, committed, retried atomic.Int64
	var wg sync.WaitGroup
	for n := 0; n < nodes; n++ {
		wg.Add(1)
		go func(node int) {
			defer wg.Done()
			seed := uint64(node)*2654435761 + 1
			rnd := func(m int64) int64 {
				seed = seed*6364136223846793005 + 1442695040888963407
				return int64((seed >> 33) % uint64(m))
			}
			for i := 0; i < txnsPerNode; i++ {
				txn := seq.Add(1)
				from, to := rnd(accounts), rnd(accounts)
				if from == to {
					continue
				}
				amount := 1 + rnd(50)
				k1, k2 := fmt.Sprintf("acct:%d", from), fmt.Sprintf("acct:%d", to)
				if k2 < k1 {
					k1, k2 = k2, k1
				}
				if err := lm.Acquire(txn, k1, aries.Exclusive); err != nil {
					lm.ReleaseAll(txn)
					retried.Add(1)
					i--
					continue
				}
				if err := lm.Acquire(txn, k2, aries.Exclusive); err != nil {
					lm.ReleaseAll(txn)
					retried.Add(1)
					i--
					continue
				}
				balances[from] -= amount
				balances[to] += amount
				lm.ReleaseAll(txn)
				committed.Add(1)
			}
		}(n)
	}
	wg.Wait()
	var sum int64
	for _, b := range balances {
		sum += b
	}
	grants, wounds := lm.Stats()
	entries, txns := lm.Pending()
	fmt.Printf("nodes=%d committed=%d wounded/retried=%d grants=%d wounds=%d\n",
		nodes, committed.Load(), retried.Load(), grants, wounds)
	fmt.Printf("balance sum=%d (invariant %d) leaked entries=%d txns=%d\n",
		sum, accounts*1000, entries, txns)
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

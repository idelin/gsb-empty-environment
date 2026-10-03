// Command aries-demo builds a WAL containing committed and
// uncommitted transactions plus a checkpoint, simulates a crash,
// and runs the ARIES three-phase recovery. It then demonstrates the
// multi-node lock arbiter under contention.
package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"aries/arbiter"
	"aries/recovery"
	"aries/store"
	"aries/wal"
)

func u64val(n uint64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], n)
	return b[:]
}

func dumpWAL(w *wal.WAL, title string) {
	fmt.Printf("--- %s ---\n", title)
	rd := w.ReaderFrom(0)
	for {
		rec, err := rd.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatalf("dump: %v", err)
		}
		switch rec.Type {
		case wal.RecUpdate, wal.RecCLR:
			fmt.Printf("LSN=%-4d %-7s txn=%d prev=%-4d page=%d before=%d after=%d undoNext=%d\n",
				rec.LSN, rec.Type, rec.TxnID, rec.PrevLSN, rec.PageID,
				binary.BigEndian.Uint64(rec.Before), binary.BigEndian.Uint64(rec.After), rec.UndoNext)
		case wal.RecCheckpoint:
			fmt.Printf("LSN=%-4d CHECKPOINT txns=%v pages=%v\n", rec.LSN, rec.Txns, rec.Pages)
		default:
			fmt.Printf("LSN=%-4d %-7s txn=%d prev=%d\n", rec.LSN, rec.Type, rec.TxnID, rec.PrevLSN)
		}
	}
	fmt.Println()
}

func recoveryDemo(dir string) {
	fmt.Println("=== ARIES crash recovery demo ===")
	walPath := filepath.Join(dir, "demo.wal")

	w, err := wal.Open(walPath)
	must(err)
	st := store.New(w)
	rt := recovery.NewRuntime(w, st)

	// Workload: T1,T2 commit; T3,T4,T5 are in flight at the crash.
	rt.Begin(1)
	rt.Begin(2)
	must2(rt.Update(1, 1, 0, u64val(0), u64val(100)))
	must2(rt.Update(2, 2, 0, u64val(0), u64val(200)))
	must2(rt.Checkpoint())
	must2(rt.Update(1, 3, 0, u64val(0), u64val(300)))
	must(rt.Commit(1))
	must(rt.End(1))
	rt.Begin(3)
	must2(rt.Update(3, 1, 0, u64val(100), u64val(150)))
	must2(rt.Update(2, 4, 0, u64val(0), u64val(400)))
	must(rt.Commit(2)) // committed, END never written
	rt.Begin(4)
	rt.Begin(5)
	must2(rt.Update(4, 2, 0, u64val(200), u64val(250)))
	must2(rt.Update(5, 5, 0, u64val(0), u64val(500)))
	must(w.Flush())

	// Pages holding uncommitted data are stolen to disk; pages 3,4,5
	// exist only in the volatile buffer pool.
	must(rt.FlushPage(1))
	must(rt.FlushPage(2))
	st.Crash()
	w.Close()
	fmt.Println("*** CRASH *** (buffer pool lost; WAL durable)")
	fmt.Println()

	w2, err := wal.Open(walPath)
	must(err)
	dumpWAL(w2, "WAL at crash time")

	st2 := store.New(w2)
	eng := &recovery.Engine{
		WAL:   w2,
		Store: st2,
		Log:   log.New(os.Stdout, "[recovery] ", 0),
	}
	must(eng.Recover())

	fmt.Println("--- durable page contents after recovery ---")
	for page := uint64(1); page <= 5; page++ {
		data, _ := st2.DiskPage(page)
		fmt.Printf("page %d = %d\n", page, binary.BigEndian.Uint64(data[:8]))
	}
	fmt.Println("expected: page1=100 page2=200 page3=300 page4=400 page5=0")
	dumpWAL(w2, "WAL after recovery (note CLRs and ENDs)")
	w2.Close()
}

func arbiterDemo() {
	fmt.Println("=== multi-node lock arbiter demo (wound-wait) ===")
	m := arbiter.NewManager()
	const nodes = 8
	const txnsPerNode = 20
	const resources = 3

	var committed, aborted int64
	var wg sync.WaitGroup
	wg.Add(nodes)
	for n := 0; n < nodes; n++ {
		go func(id uint64) {
			defer wg.Done()
			for tx := 0; tx < txnsPerNode; tx++ {
				m.Begin(id)
				ok := true
				for r := 0; r < resources; r++ {
					if err := m.Lock(id, uint64(r), arbiter.Exclusive); err != nil {
						if !errors.Is(err, arbiter.ErrAborted) {
							log.Fatalf("lock: %v", err)
						}
						ok = false
						break
					}
				}
				if ok {
					atomic.AddInt64(&committed, 1)
				} else {
					atomic.AddInt64(&aborted, 1)
				}
				m.Finish(id)
			}
		}(uint64(n + 1))
	}
	wg.Wait()
	fmt.Printf("%d nodes x %d txns on %d resources: committed=%d aborted(wounded)=%d\n",
		nodes, txnsPerNode, resources, committed, aborted)
	fmt.Println("no deadlock, no leaked waiters:", m)
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func must2(_ uint64, err error) {
	must(err)
}

func main() {
	dir, err := os.MkdirTemp("", "aries-demo-*")
	must(err)
	defer os.RemoveAll(dir)
	recoveryDemo(dir)
	arbiterDemo()
}

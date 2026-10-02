// Demo: a read-only transaction scans the whole table while a concurrent
// writer splits nodes. The scan only ever sees the pre-write snapshot.
package main

import (
	"fmt"

	"cowtree"
)

func main() {
	db := cowtree.Open()

	const base = int64(1000)
	for k := int64(0); k < base; k++ {
		db.Put(k, k*10)
	}
	fmt.Printf("preloaded %d keys, root page #%d, %d pages allocated\n",
		base, db.RootPageID(), db.PagesAllocated())

	// Open a read-only transaction (captures the current root, no locks).
	snap := db.View()
	fmt.Printf("read-only tx began at root page #%d\n", snap.RootPageID())

	// Concurrent writer: inserts 5x more keys, forcing many page splits.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for k := base; k < base*6; k++ {
			db.Put(k, k*10)
		}
	}()

	// Meanwhile the read-only tx scans the full table, repeatedly.
	scans, keys := 0, 0
	for {
		select {
		case <-done:
			goto finished
		default:
		}
		it := snap.Iterator()
		prev, count := int64(-1), 0
		for it.Next() {
			k := it.Key()
			if k <= prev {
				panic("scan out of order")
			}
			prev, count = k, count+1
		}
		if count != int(base) {
			panic(fmt.Sprintf("snapshot leaked/torn: saw %d keys, want %d", count, base))
		}
		scans++
		keys = count
	}
finished:
	<-done

	fmt.Printf("during the writer's splits, snapshot completed %d full scans, each saw exactly %d keys\n",
		scans, keys)
	fmt.Printf("snapshot still rooted at page #%d; live root is page #%d\n",
		snap.RootPageID(), db.RootPageID())
	fmt.Printf("writer committed %d keys total, %d pages allocated (old pages never touched)\n",
		base*6, db.PagesAllocated())

	// Prove point-in-time reads: snapshot misses new keys, live DB has them.
	if _, ok := snap.Get(base + 1); ok {
		panic("snapshot sees post-snapshot write")
	}
	if _, ok := db.Get(base + 1); !ok {
		panic("live DB missing committed key")
	}
	fmt.Println("snapshot is a consistent point-in-time view: OK")
}

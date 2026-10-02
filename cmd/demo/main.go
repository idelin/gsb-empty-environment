// Demo of lock-free snapshot reads over a CoW B+ tree: a reader scans a pinned
// snapshot while a concurrent writer splits pages and publishes new roots.
package main

import (
	"fmt"
	"sync"

	"cowtree"
)

func main() {
	tr := cowtree.New()

	// 1. Load initial data and take a read-only snapshot (pins the old root).
	for i := 0; i < 20; i++ {
		tr.Put(i, fmt.Sprintf("v%02d", i))
	}
	snap := tr.Snapshot()
	fmt.Printf("[t0] snapshot taken: count=%d height=%d\n", snap.Count(), snap.Height())

	// 2. Concurrent writer: inserts 5,000 keys -> constant page splits.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 20; i < 5020; i++ {
			tr.Put(i, fmt.Sprintf("v%04d", i))
		}
	}()

	// 3. Meanwhile, scan the old snapshot repeatedly: always the same view.
	scans := 0
	for tr.Snapshot().Count() < 5020 { // poll until writer is done
		if n := snap.Count(); n != 20 {
			fmt.Printf("FAIL: snapshot scan saw %d keys, want 20\n", n)
			return
		}
		scans++
	}
	wg.Wait()
	fmt.Printf("[t1] writer finished 5000 inserts (with splits); "+
		"old snapshot scanned %d times, always exactly 20 keys\n", scans)

	// 4. Old snapshot vs. latest view.
	keys, vals := snap.Collect()
	fmt.Printf("[old snapshot] count=%d  keys=%v\n", snap.Count(), keys)
	fmt.Printf("[old snapshot] Get(5)=%q\n", vals[5])

	latest := tr.Snapshot()
	fmt.Printf("[latest view ] count=%d height=%d\n", latest.Count(), latest.Height())
	v, _ := latest.Get(5)
	fmt.Printf("[latest view ] Get(5)=%q  Get(5000)=", v)
	v, _ = latest.Get(5000)
	fmt.Printf("%q\n", v)

	// 5. Small structural demo of CoW root swap.
	small := cowtree.New()
	for i := 0; i < 9; i++ {
		small.Put(i, fmt.Sprintf("x%d", i))
	}
	fmt.Print("\nsmall tree after 9 inserts (MaxEntries=8, root split):\n")
	fmt.Print(small.Snapshot().String())
}

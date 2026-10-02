// Command example demonstrates the concurrent skip list: random inserts,
// a range scan, and a snapshot iterator that stays stable while a
// background goroutine keeps writing.
package main

import (
	"fmt"
	"math/rand"
	"sync"
	"time"

	"skiplist"
)

func main() {
	sl := skiplist.New()

	// 1. Insert 10,000 random key-value pairs concurrently.
	const n = 10000
	keys := rand.Perm(n)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := w; i < n; i += 8 {
				sl.Insert(int64(keys[i]), int64(keys[i]*10))
			}
		}(w)
	}
	wg.Wait()
	fmt.Printf("inserted %d key-value pairs concurrently, Len = %d\n", n, sl.Len())

	// 2. Range scan: print a small window in sorted order.
	fmt.Print("range scan [4995, 5005]:")
	for it := sl.NewIterator(4995, 5005); it.Valid(); it.Next() {
		fmt.Printf(" %d=>%d", it.Key(), it.Value())
	}
	fmt.Println()

	// 3. Snapshot isolation: open an iterator, then keep writing while
	//    traversing; the output must be exactly the pre-existing keys.
	snap := sl.NewIterator(0, 2*n)
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := n; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			sl.Insert(int64(i), int64(i))
		}
	}()
	// Let the writer run for a while before and during traversal.
	time.Sleep(100 * time.Millisecond)
	count, prev, ok := 0, int64(-1), true
	for ; snap.Valid(); snap.Next() {
		if snap.Key() <= prev {
			ok = false
			break
		}
		prev = snap.Key()
		count++
		if count == n/2 {
			time.Sleep(100 * time.Millisecond)
		}
	}
	close(stop)
	wg.Wait()
	fmt.Printf("snapshot iterator under concurrent writes: %d keys, strictly increasing = %v, isolated = %v\n",
		count, ok, count == n)
	fmt.Printf("after writes, Len = %d (fresh iterators see the new keys)\n", sl.Len())
}

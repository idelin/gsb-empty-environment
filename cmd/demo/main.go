// demo 演示螃蟹脚并发 B+ 树：10 个读线程 + 5 个写线程并发工作。
package main

import (
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"latchcrab"
)

func main() {
	tree := latchcrab.New(8)
	const (
		readers   = 10
		writers   = 5
		perWriter = 20000
		keySpace  = readers * writers * 1000
	)

	var reads, writes int64
	start := time.Now()
	var wg sync.WaitGroup

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(w)))
			for i := 0; i < perWriter; i++ {
				tree.Insert(rng.Intn(keySpace))
				atomic.AddInt64(&writes, 1)
			}
		}(w)
	}
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func(r int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(r + 1000)))
			deadline := start.Add(2 * time.Second)
			for time.Now().Before(deadline) {
				tree.Get(rng.Intn(keySpace))
				atomic.AddInt64(&reads, 1)
			}
		}(r)
	}
	wg.Wait()

	elapsed := time.Since(start)
	fmt.Printf("threads: %d readers + %d writers\n", readers, writers)
	fmt.Printf("inserts: %d (%.0f ops/s)\n", writes, float64(writes)/elapsed.Seconds())
	fmt.Printf("reads:   %d (%.0f ops/s)\n", reads, float64(reads)/elapsed.Seconds())
	fmt.Printf("sample Get(42)=%v, Range(10,15)=%v\n", tree.Get(42), tree.Range(10, 15))
	fmt.Println("completed without deadlock")
}

// Demo: 100 concurrent workers acquiring from a lock-free CAS token bucket.
package main

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"tokenbucket"
)

func main() {
	const (
		rate     = 1_000_000.0 // 1M tokens per second
		capacity = 100_000
		workers  = 100
		duration = time.Second
	)

	b := tokenbucket.New(rate, capacity)

	var granted atomic.Int64
	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for time.Since(start) < duration {
				if b.Acquire() {
					granted.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	// Theoretical supply over the run: initial capacity + rate * elapsed.
	theoretical := int64(capacity) + int64(rate*elapsed.Seconds())
	fmt.Printf("workers:           %d\n", workers)
	fmt.Printf("duration:          %v\n", elapsed)
	fmt.Printf("tokens granted:    %d\n", granted.Load())
	fmt.Printf("theoretical max:   %d (capacity %d + %.0f/s x %v)\n", theoretical, capacity, rate, elapsed)
	fmt.Printf("within bound:      %v\n", granted.Load() <= theoretical+workers)
	fmt.Printf("tokens remaining:  %d\n", b.Available())
}

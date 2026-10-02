// Demo: 100 concurrent workers hammer a lock-free CAS-based token bucket.
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
		workers    = 100
		capacity   = 100
		ratePerSec = 50_000
		duration   = time.Second
	)
	bucket := tokenbucket.NewTokenBucket(capacity, ratePerSec)

	var granted atomic.Int64
	var stop atomic.Bool
	var wg sync.WaitGroup

	start := time.Now()
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				if bucket.TryAcquire() {
					granted.Add(1)
				}
			}
		}()
	}
	time.Sleep(duration)
	stop.Store(true)
	wg.Wait()
	elapsed := time.Since(start).Seconds()

	theoreticalMax := int64(capacity) + int64(ratePerSec*elapsed) + ratePerSec/100
	fmt.Printf("workers:          %d\n", workers)
	fmt.Printf("duration:         %.3fs\n", elapsed)
	fmt.Printf("total granted:    %d tokens\n", granted.Load())
	fmt.Printf("theoretical max:  %d tokens (capacity + rate*elapsed)\n", theoreticalMax)
	fmt.Printf("within theory:    %v\n", granted.Load() <= theoreticalMax)
	fmt.Printf("tokens remaining: %.2f\n", bucket.Tokens())
}

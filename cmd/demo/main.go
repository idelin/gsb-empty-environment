// Demo: invalidation storm coalescing, priority refresh and queue overflow.
package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"coalesce"
)

func main() {
	var (
		mu     sync.Mutex
		loads  []string
		nloads atomic.Int64
	)

	iv := coalesce.New(coalesce.Options{
		// Injectable data source: here a fake "database" that just echoes.
		Load: func(_ context.Context, key string) (any, error) {
			mu.Lock()
			loads = append(loads, key)
			mu.Unlock()
			n := nloads.Add(1)
			time.Sleep(5 * time.Millisecond) // pretend I/O
			return fmt.Sprintf("value-of-%s#%d", key, n), nil
		},
		IsCritical: func(key string) bool { return strings.HasPrefix(key, "config") },
		OnError:    func(key string, err error) { fmt.Printf("load %s failed: %v\n", key, err) },
		Capacity:   6, // small queue to demonstrate overflow
		Interval:   30 * time.Millisecond,
	})
	defer iv.Close()

	fmt.Println("== storm: 1000 invalidations of hot-key ==")
	for v := uint64(1); v <= 1000; v++ {
		iv.Invalidate("hot-key", v)
	}

	fmt.Println("== while worker is busy: 5 normal keys + 1 critical + 1 overflow ==")
	iv.Invalidate("user:1", 1)
	iv.Invalidate("user:2", 1)
	iv.Invalidate("user:3", 1)
	iv.Invalidate("user:4", 1)
	iv.Invalidate("config:feature-flags", 1) // critical: jumps the queue
	iv.Invalidate("user:5", 1)               // fills the queue (capacity 6)
	iv.Invalidate("user:6", 1)               // overflow: conservatively invalid

	if _, ok := iv.Get("user:6"); !ok {
		fmt.Println(`Get("user:6") = MISS (overflowed -> conservatively invalid, stale value never served)`)
	}

	// Wait for the rate-limited worker to drain.
	time.Sleep(800 * time.Millisecond)

	mu.Lock()
	fmt.Printf("load order: %v\n", loads)
	mu.Unlock()
	fmt.Printf("total loads: %d (1000 invalidations of hot-key coalesced into 1)\n", nloads.Load())

	for _, k := range []string{"hot-key", "config:feature-flags", "user:6"} {
		st := iv.Inspect(k)
		if v, ok := iv.Get(k); ok {
			fmt.Printf("Get(%q) = %v (validVersion=%d)\n", k, v, st.ValidVersion)
		} else {
			fmt.Printf("Get(%q) = MISS\n", k)
		}
	}
}

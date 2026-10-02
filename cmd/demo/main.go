// Command demo runs a live simulation of the dual-ring hot-standby
// controller: continuous traffic on the active ring, a background
// scale-in injected into the standby ring, warmup, and an atomic flip.
package main

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"hotring"
)

func main() {
	const nodeCount = 6
	nodes := make([]string, 0, nodeCount)
	for i := 0; i < nodeCount; i++ {
		nodes = append(nodes, fmt.Sprintf("cache-%d", i))
	}
	controller := hotring.NewController(nodes, 160)
	fmt.Printf("[boot] active ring serving %d nodes: %v\n", nodeCount, nodes)

	// --- traffic generator: full load on the active ring ---
	var stop int32
	var routes, errs int64
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; atomic.LoadInt32(&stop) == 0; i++ {
				if _, err := controller.Route(fmt.Sprintf("w%d-key-%d", id, i)); err != nil {
					atomic.AddInt64(&errs, 1)
				}
				atomic.AddInt64(&routes, 1)
			}
		}(w)
	}
	time.Sleep(50 * time.Millisecond)
	fmt.Printf("[traffic] %d goroutines routing keys through the active ring\n", 4)

	// --- background: inject scale-in into the standby ring ---
	standby := nodes[:nodeCount-1] // cache-5 decommissioned
	controller.PrepareStandby(standby)
	fmt.Printf("[standby] topology change injected (scale-in): %v\n", standby)

	// --- warmup: precompute key-migration impact off the live path ---
	keys := make([]string, 0, 100000)
	for i := 0; i < 100000; i++ {
		keys = append(keys, fmt.Sprintf("known-key-%d", i))
	}
	warmStart := time.Now()
	report, err := controller.Warmup(keys)
	if err != nil {
		panic(err)
	}
	fmt.Printf("[warmup] done in %s: %d/%d keys (%.2f%%) will migrate\n",
		time.Since(warmStart), report.MovedKeys, report.SampledKeys,
		report.MovedRatio()*100)
	for node, n := range report.LostBy {
		fmt.Printf("         node %s loses %d keys\n", node, n)
	}

	// --- atomic pointer flip ---
	flipStart := time.Now()
	if _, err := controller.Flip(); err != nil {
		panic(err)
	}
	flipCost := time.Since(flipStart)
	fmt.Printf("[flip] atomic pointer switch completed in %s\n", flipCost)

	time.Sleep(50 * time.Millisecond)
	atomic.StoreInt32(&stop, 1)
	wg.Wait()

	flips, last := controller.Stats()
	fmt.Printf("[done] %d total routes, %d errors, flips=%d last-flip=%s\n",
		routes, errs, flips, time.Unix(0, last).Format(time.RFC3339Nano))
	fmt.Printf("[done] active topology now: %v\n", controller.ActiveRing().Members())
	if errs != 0 {
		panic("routing errors observed during flip")
	}
	fmt.Println("[ok] zero transient misrouting across the flip")
}

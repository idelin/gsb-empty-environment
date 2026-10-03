// Demo of the dual-ring hot-standby routing controller:
// steady traffic on the active ring -> scale-in staged on the standby ring
// -> prewarm (migration impact precomputed) -> atomic flip -> smooth cutover.
package main

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"hashring"
)

func main() {
	// 1. Bootstrap: 12 nodes across 3 zones / 6 racks.
	b := hashring.NewBuilder(160)
	for i := 0; i < 12; i++ {
		_ = b.Add(hashring.Node{
			ID:   fmt.Sprintf("node-%02d", i),
			Zone: fmt.Sprintf("zone-%c", 'a'+i%3),
			Rack: fmt.Sprintf("rack-%d", i%6),
		})
	}
	ctl, err := hashring.NewController(b)
	if err != nil {
		panic(err)
	}
	fmt.Printf("[boot] active ring v%d: %d nodes, serving 100%% traffic\n",
		ctl.Version(), ctl.Active().NumNodes())

	// 2. Steady-state traffic on the active ring (concurrent readers).
	var lookups, routeErrors atomic.Uint64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				key := fmt.Sprintf("user:%d:%d", w, i)
				n, ok := ctl.Lookup(key)
				// A routing error = empty result, or a node that is not a
				// member of the ring version actually read.
				ring := ctl.Active()
				if !ok || !ring.Has(n.ID) {
					routeErrors.Add(1)
				}
				lookups.Add(1)
			}
		}(w)
	}
	time.Sleep(200 * time.Millisecond)
	fmt.Printf("[traffic] %d lookups served on active ring, errors=%d\n",
		lookups.Load(), routeErrors.Load())

	// 3. Inject scale-in on the STANDBY ring (active traffic untouched).
	t0 := time.Now()
	for _, id := range []string{"node-03", "node-07", "node-11"} {
		if err := ctl.SyncRemove(id); err != nil {
			panic(err)
		}
	}
	fmt.Printf("[standby] staged scale-in: -3 nodes (%s)\n", time.Since(t0))

	// 4. Prewarm: build standby ring + precompute migration impact.
	t0 = time.Now()
	plan, err := ctl.Prewarm(200000)
	if err != nil {
		panic(err)
	}
	fmt.Printf("[prewarm] standby ring v%d built in %s\n", plan.ToVersion, time.Since(t0))
	fmt.Printf("[prewarm] removed=%v\n", plan.RemovedNodes)
	fmt.Printf("[prewarm] exact hash-space moved: %.4f%% (sampled: %.4f%% over %d keys)\n",
		plan.HashSpaceMoved*100, plan.SampleMoveFraction()*100, plan.Sampled)

	// 5. Atomic flip: single pointer store, measured.
	t0 = time.Now()
	if _, err := ctl.Flip(); err != nil {
		panic(err)
	}
	flipCost := time.Since(t0)
	fmt.Printf("[flip] pointer swapped in %s; now serving ring v%d with %d nodes\n",
		flipCost, ctl.Version(), ctl.Active().NumNodes())

	// 6. Let traffic run on the new ring, then verify zero routing errors.
	time.Sleep(200 * time.Millisecond)
	close(stop)
	wg.Wait()
	fmt.Printf("[done] total lookups=%d, routing errors=%d\n",
		lookups.Load(), routeErrors.Load())

	// 7. Post-flip sanity: removed nodes never serve traffic again.
	if _, ok := ctl.Active().Node("node-03"); ok {
		panic("removed node still on active ring")
	}
	n, _ := ctl.Lookup("user:0:1")
	replicas := ctl.LookupN("user:0:1", 3)
	fmt.Printf("[verify] key user:0:1 -> %s, replicas=%v\n", n.ID, replicaIDs(replicas))
}

func replicaIDs(ns []hashring.Node) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = n.ID + "(" + n.Zone + ")"
	}
	return out
}

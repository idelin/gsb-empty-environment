// Command sim runs a live simulation of the hierarchical lease
// coordinator: 1 root, N region gateways, M edge workers. It prints
// root vs gateway RPC counts to demonstrate that root load stays flat
// regardless of edge churn, and that dropped workers are reclaimed
// locally by their gateway.
//
// Workers run a fixed number of request/renew/release cycles (no
// sleeps) so the demo also works on Go <= 1.13, where a single runtime
// goroutine manages all timers and 1000 sleeping goroutines would
// collapse it. Timings are accelerated by default; pass
// -root-ttl=1m -sub-ttl=5s for production-shaped durations.
package main

import (
	"flag"
	"fmt"
	"runtime"
	"sync"
	"time"

	"hlease/lease"
)

func main() {
	workers := flag.Int("workers", 1000, "number of edge worker nodes")
	regions := flag.Int("regions", 5, "number of region gateways")
	rootTTL := flag.Duration("root-ttl", 1*time.Second, "root->gateway lease TTL (spec: 1m)")
	subTTL := flag.Duration("sub-ttl", 200*time.Millisecond, "gateway->worker sub-lease TTL (spec: 5s)")
	cycles := flag.Int("cycles", 5, "request/renew/release cycles per worker")
	dropPct := flag.Int("drop-pct", 10, "percent of workers that drop offline mid-run")
	flag.Parse()

	fmt.Printf("=== hierarchical lease coordinator simulation ===\n")
	fmt.Printf("workers=%d regions=%d root-ttl=%v sub-ttl=%v cycles=%d drop=%d%%\n\n",
		*workers, *regions, *rootTTL, *subTTL, *cycles, *dropPct)

	root := lease.NewRootCoordinator(int64(*workers))

	gws := make([]*lease.Gateway, *regions)
	for i := 0; i < *regions; i++ {
		gws[i] = lease.NewGateway(fmt.Sprintf("region-%d", i), root,
			int64(*workers / *regions), *rootTTL, *subTTL)
		if err := gws[i].Start(); err != nil {
			fmt.Printf("gateway %d failed to start: %v\n", i, err)
			return
		}
	}
	defer func() {
		for _, g := range gws {
			g.Stop()
		}
	}()

	rootBefore := root.Requests()

	var wg sync.WaitGroup
	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			gw := gws[id%*regions]
			workerID := fmt.Sprintf("worker-%d", id)
			for c := 0; c < *cycles; c++ {
				// Mid-run, a wave of workers drops offline: they simply
				// stop and never release their current sub-lease.
				if id%100 < *dropPct && c == *cycles/2 {
					sub, err := gw.RequestSubLease(workerID, 1)
					if err == nil {
						_ = sub // crash: lease held, never released
					}
					return
				}
				var sub *lease.Lease
				for {
					l, err := gw.RequestSubLease(workerID, 1)
					if err == nil {
						sub = l
						break
					}
					runtime.Gosched()
				}
				_, _ = gw.RenewSubLease(sub.ID)
				gw.ReleaseSubLease(sub.ID)
			}
		}(i)
	}
	wg.Wait()

	rootAfterStorm := root.Requests()

	// Give gateways a few sub-lease TTLs to reap the dropped workers.
	time.Sleep(3 * *subTTL)

	var gwRequests, gwReaped int64
	for _, g := range gws {
		gwRequests += g.Requests()
		gwReaped += g.Reaped()
	}

	fmt.Printf("--- results ---\n")
	fmt.Printf("gateway RPCs handled (edge load):   %d\n", gwRequests)
	fmt.Printf("sub-leases reclaimed after drops:   %d\n", gwReaped)
	fmt.Printf("root RPCs before edge storm:        %d\n", rootBefore)
	fmt.Printf("root RPCs after edge storm:         %d (delta=%d)\n",
		rootAfterStorm, rootAfterStorm-rootBefore)
	fmt.Printf("root RPCs after local reaping:      %d (delta during reap=%d)\n",
		root.Requests(), root.Requests()-rootAfterStorm)
	fmt.Printf("root active leases (gateways only): %d\n", root.ActiveLeases())
	fmt.Printf("root quota used/total:              %d/%d\n", root.UsedQuota(), root.TotalQuota())
}

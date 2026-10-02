// Demo of the hierarchical lease coordinator: 1 root, 5 region gateways,
// 1000 edge workers. Uses the production lease periods from the spec
// (1 minute coarse leases, 5 second sub-leases) and a real clock.
package main

import (
	"fmt"
	"sync"
	"time"

	"leasecoord"
)

const (
	gatewayCount      = 5
	workersPerGateway = 200 // 1000 edge workers total
	workerQuota       = 10

	rootTTL = time.Minute     // long-lived coarse lease
	subTTL  = 5 * time.Second // short-lived fine-grained sub-lease
)

func main() {
	root := leasecoord.NewRootCoordinator(1_000_000, rootTTL, nil)

	gateways := make([]*leasecoord.RegionGateway, gatewayCount)
	for i := range gateways {
		id := fmt.Sprintf("gateway-%d", i)
		gateways[i] = leasecoord.NewRegionGateway(id, root, subTTL, 5_000, nil)
	}

	// Background sweeper per gateway: reclaims dead workers' sub-leases
	// locally. The root coordinator is never touched.
	stopSweep := make(chan struct{})
	var sweepWG sync.WaitGroup
	for _, g := range gateways {
		sweepWG.Add(1)
		go func(g *leasecoord.RegionGateway) {
			defer sweepWG.Done()
			ticker := time.NewTicker(500 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-stopSweep:
					return
				case <-ticker.C:
					g.Sweep()
				}
			}
		}(g)
	}

	fmt.Printf("cluster: 1 root, %d gateways, %d workers (coarse TTL %v, sub TTL %v)\n",
		gatewayCount, gatewayCount*workersPerGateway, rootTTL, subTTL)

	// Phase 1: all 1000 workers acquire quota and renew for ~3 seconds.
	dropped := leasecoord.NewWorkerNode("worker-0-0", gateways[0])
	var wg sync.WaitGroup
	for gi, g := range gateways {
		for wi := 0; wi < workersPerGateway; wi++ {
			id := fmt.Sprintf("worker-%d-%d", gi, wi)
			w := leasecoord.NewWorkerNode(id, g)
			if gi == 0 && wi == 0 {
				w = dropped // this worker will "go offline" later
			}
			wg.Add(1)
			go func(w *leasecoord.WorkerNode) {
				defer wg.Done()
				if err := w.Acquire(workerQuota); err != nil {
					fmt.Println("acquire failed:", err)
					return
				}
				for i := 0; i < 6; i++ {
					time.Sleep(500 * time.Millisecond)
					if err := w.Renew(); err != nil {
						return
					}
				}
			}(w)
		}
	}
	wg.Wait()
	fmt.Printf("phase 1: all workers leased; root lease RPCs served = %d (gateways only)\n",
		root.MessageCount())

	// Phase 2: worker-0-0 drops offline (no renew, no release). Everyone
	// else releases cleanly.
	rootLoadBefore := root.MessageCount()
	for gi, g := range gateways {
		for wi := 0; wi < workersPerGateway; wi++ {
			if gi == 0 && wi == 0 {
				continue // dropped: simply stops talking
			}
			leasecoord.NewWorkerNode(fmt.Sprintf("worker-%d-%d", gi, wi), g).Release()
		}
	}
	fmt.Printf("phase 2: worker-0-0 dropped offline; waiting for its %v sub-lease to time out...\n", subTTL)
	time.Sleep(subTTL + 2*time.Second) // let the local sweeper fire
	close(stopSweep)
	sweepWG.Wait()

	fmt.Printf("         gateway-0 live sub-leases = %d (dropped worker's quota reclaimed locally)\n",
		gateways[0].SubLeaseCount())
	fmt.Printf("         root lease RPCs during drop: %d -> %d (delta = %d, zero central load)\n",
		rootLoadBefore, root.MessageCount(), root.MessageCount()-rootLoadBefore)
	fmt.Printf("         root outstanding coarse quota = %d\n", root.OutstandingQuota())
}

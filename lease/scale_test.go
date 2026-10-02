package lease

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

// TestScaleThousandWorkersFiveGateways is the scale acceptance test:
// 1000 edge workers across 5 region gateways generate heavy sub-lease
// traffic, while the root only ever talks to the 5 gateways and its
// request count stays tiny and flat.
//
// Workers run fixed request/renew/release cycles instead of sleeping:
// on Go <= 1.13 all timers share a single runtime goroutine, so 1000
// goroutines sleeping in a tight loop would collapse the timer heap
// and skew the very load numbers this test measures.
func TestScaleThousandWorkersFiveGateways(t *testing.T) {
	const (
		workers = 1000
		regions = 5
		cycles  = 3
		rootTTL = 300 * time.Millisecond // stands in for 1 minute
		subTTL  = 50 * time.Millisecond  // stands in for 5 seconds
	)

	root := NewRootCoordinator(workers)

	gws := make([]*Gateway, regions)
	for i := 0; i < regions; i++ {
		gws[i] = NewGateway(fmt.Sprintf("region-%d", i), root, workers/regions, rootTTL, subTTL)
		if err := gws[i].Start(); err != nil {
			t.Fatalf("start gateway %d: %v", i, err)
		}
	}
	defer func() {
		for _, g := range gws {
			g.Stop()
		}
	}()

	// Snapshot root load before the edge storm begins.
	rootBefore := root.Requests()

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			gw := gws[id%regions]
			workerID := fmt.Sprintf("worker-%d", id)
			for c := 0; c < cycles; c++ {
				var sub *Lease
				for {
					l, err := gw.RequestSubLease(workerID, 1)
					if err == nil {
						sub = l
						break
					}
					// Region momentarily full or root lease mid-renewal:
					// back off cooperatively and retry.
					runtime.Gosched()
				}
				_, _ = gw.RenewSubLease(sub.ID)
				gw.ReleaseSubLease(sub.ID)
			}
		}(i)
	}
	wg.Wait()

	var gwRequests, gwReaped int64
	for _, g := range gws {
		gwRequests += g.Requests()
		gwReaped += g.Reaped()
	}
	rootAfter := root.Requests()

	t.Logf("edge workers:            %d", workers)
	t.Logf("region gateways:         %d", regions)
	t.Logf("gateway RPCs handled:    %d (edge load absorbed locally)", gwRequests)
	t.Logf("sub-leases reaped:       %d", gwReaped)
	t.Logf("root RPCs before storm:  %d", rootBefore)
	t.Logf("root RPCs after storm:   %d (gateways only)", rootAfter)
	t.Logf("root active leases:      %d", root.ActiveLeases())

	if gwRequests < workers {
		t.Fatalf("gateways handled only %d RPCs, want >= %d", gwRequests, workers)
	}
	// Root traffic must be bounded by gateway-level chatter only:
	// 5 acquires + a handful of renewals, nowhere near the edge volume.
	if delta := rootAfter - rootBefore; delta > 100 {
		t.Fatalf("root handled %d RPCs during edge storm, want <= 100 (near-zero load)", delta)
	}
	if got := root.ActiveLeases(); got > regions {
		t.Fatalf("root tracks %d leases, want <= %d (gateways only)", got, regions)
	}
}

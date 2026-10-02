package leasecoord

import (
	"fmt"
	"testing"
	"time"
)

// Production lease periods from the spec.
const (
	rootTTL = time.Minute
	subTTL  = 5 * time.Second
)

func setupCluster(totalQuota int64, gatewayCount int, chunkSize int64, clock Clock) (*RootCoordinator, []*RegionGateway) {
	root := NewRootCoordinator(totalQuota, rootTTL, clock)
	gateways := make([]*RegionGateway, gatewayCount)
	for i := range gateways {
		gateways[i] = NewRegionGateway(fmt.Sprintf("gateway-%d", i), root, subTTL, chunkSize, clock)
	}
	return root, gateways
}

// Acceptance: 1000 edge workers request quota, but the root coordinator
// only maintains lease communication with the 5 region gateways.
func TestThousandWorkersRootOnlyTalksToFiveGateways(t *testing.T) {
	clock := NewManualClock(time.Now())
	root, gateways := setupCluster(1_000_000, 5, 5_000, clock)

	const workersPerGateway = 200 // 5 * 200 = 1000 edge workers
	const workerQuota = 10
	for gi, g := range gateways {
		for wi := 0; wi < workersPerGateway; wi++ {
			w := NewWorkerNode(fmt.Sprintf("worker-%d-%d", gi, wi), g)
			if err := w.Acquire(workerQuota); err != nil {
				t.Fatalf("worker %d-%d acquire: %v", gi, wi, err)
			}
		}
	}

	if got := root.MessageCount(); got != 5 {
		t.Fatalf("root served %d lease RPCs, want exactly 5 (one per gateway)", got)
	}
	var totalSubLeases, totalQuota int64
	for _, g := range gateways {
		totalSubLeases += int64(g.SubLeaseCount())
		totalQuota += g.OutstandingQuota()
	}
	if totalSubLeases != 1000 {
		t.Fatalf("live sub-leases = %d, want 1000", totalSubLeases)
	}
	if want := int64(1000 * workerQuota); totalQuota != want {
		t.Fatalf("outstanding worker quota = %d, want %d", totalQuota, want)
	}
}

// Acceptance: an edge node going offline only causes its local sub-lease to
// time out and be released by its gateway; root load shows zero fluctuation.
func TestWorkerDropReclaimsQuotaLocally(t *testing.T) {
	clock := NewManualClock(time.Now())
	root, gateways := setupCluster(10_000, 2, 1_000, clock)
	g := gateways[0]

	alive := NewWorkerNode("alive", g)
	dropped := NewWorkerNode("dropped", g)
	if err := alive.Acquire(10); err != nil {
		t.Fatal(err)
	}
	if err := dropped.Acquire(10); err != nil {
		t.Fatal(err)
	}
	if got := g.SubLeaseCount(); got != 2 {
		t.Fatalf("sub-leases = %d, want 2", got)
	}

	rootLoadBefore := root.MessageCount()

	// The dropped worker goes offline: no renew, no release. Advance past
	// the 5s sub-lease TTL so its sub-lease times out.
	clock.Advance(subTTL + time.Second)

	// Local activity (a renewal from the surviving worker) triggers the
	// gateway's lazy expiry sweep.
	if err := alive.Renew(); err != nil {
		t.Fatalf("alive worker renew: %v", err)
	}
	g.Sweep()

	if got := g.SubLeaseCount(); got != 1 {
		t.Fatalf("sub-leases after drop = %d, want 1 (only alive)", got)
	}
	if got := g.OutstandingQuota(); got != 10 {
		t.Fatalf("outstanding quota after drop = %d, want 10 (dropped quota reclaimed)", got)
	}
	if got := root.MessageCount(); got != rootLoadBefore {
		t.Fatalf("root load fluctuated: %d RPCs before drop, %d after", rootLoadBefore, got)
	}
}

// A gateway whose coarse lease lapses re-acquires it from the root on the
// next local demand; the root reclaims expired coarse quota.
func TestCoarseLeaseExpiryAndReacquire(t *testing.T) {
	clock := NewManualClock(time.Now())
	root, gateways := setupCluster(10_000, 1, 1_000, clock)
	g := gateways[0]

	w := NewWorkerNode("w", g)
	if err := w.Acquire(10); err != nil {
		t.Fatal(err)
	}
	if got := root.OutstandingQuota(); got != 1_000 {
		t.Fatalf("root outstanding = %d, want 1000 (one chunk)", got)
	}

	clock.Advance(rootTTL + time.Second)
	if got := root.OutstandingQuota(); got != 0 {
		t.Fatalf("root outstanding after coarse TTL = %d, want 0 (reclaimed)", got)
	}

	w2 := NewWorkerNode("w2", g)
	if err := w2.Acquire(10); err != nil {
		t.Fatalf("re-acquire after coarse expiry: %v", err)
	}
	if got := root.OutstandingQuota(); got != 1_000 {
		t.Fatalf("root outstanding after re-acquire = %d, want 1000", got)
	}
}

// Renewing before the TTL keeps a sub-lease alive; missing the TTL drops it.
func TestSubLeaseRenewal(t *testing.T) {
	clock := NewManualClock(time.Now())
	_, gateways := setupCluster(10_000, 1, 1_000, clock)
	g := gateways[0]

	w := NewWorkerNode("w", g)
	if err := w.Acquire(10); err != nil {
		t.Fatal(err)
	}
	clock.Advance(subTTL - time.Second) // 4s: renew just in time
	if err := w.Renew(); err != nil {
		t.Fatal(err)
	}
	clock.Advance(subTTL - time.Second) // 8s total, 4s since renew
	if got := g.SubLeaseCount(); got != 1 {
		t.Fatalf("sub-leases after timely renew = %d, want 1", got)
	}
	clock.Advance(2 * time.Second) // 6s since renew: expired
	if got := g.SubLeaseCount(); got != 0 {
		t.Fatalf("sub-leases after missed renew = %d, want 0", got)
	}
}

// The root enforces the cluster-wide quota ceiling across gateways.
func TestRootQuotaExhaustion(t *testing.T) {
	clock := NewManualClock(time.Now())
	_, gateways := setupCluster(1_500, 2, 1_000, clock)

	w1 := NewWorkerNode("w1", gateways[0])
	if err := w1.Acquire(10); err != nil {
		t.Fatalf("first gateway should get a chunk: %v", err)
	}
	w2 := NewWorkerNode("w2", gateways[1])
	if err := w2.Acquire(10); err == nil {
		t.Fatal("second gateway should be denied: root quota exhausted")
	}
}

// Concurrent workers across gateways never overload the root.
func TestConcurrentWorkers(t *testing.T) {
	clock := NewManualClock(time.Now())
	root, gateways := setupCluster(1_000_000, 5, 5_000, clock)

	done := make(chan struct{})
	for gi, g := range gateways {
		go func(gi int, g *RegionGateway) {
			for wi := 0; wi < 200; wi++ {
				w := NewWorkerNode(fmt.Sprintf("worker-%d-%d", gi, wi), g)
				if err := w.Acquire(10); err != nil {
					t.Errorf("acquire: %v", err)
					return
				}
			}
			done <- struct{}{}
		}(gi, g)
	}
	for range gateways {
		<-done
	}
	if got := root.MessageCount(); got != 5 {
		t.Fatalf("root served %d lease RPCs under concurrency, want 5", got)
	}
}

// Command demo runs the lease-read optimizer through the acceptance
// scenario: 10k concurrent reads inside the lease safe window (zero network
// overhead), then the automatic degrade to quorum heartbeats when the
// remaining lease drops below 100ms.
package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"leaseread/leaseread"
)

func main() {
	fmt.Println("=== Lease Read Optimizer Demo ===")
	fmt.Println("cluster: 1 leader + 2 followers, lease=1s, degrade threshold=100ms")

	cluster := leaseread.NewCluster(leaseread.RealClock{}, 2, 200*time.Microsecond, 1*time.Second, leaseread.DefaultDegradeThreshold)
	cluster.Store.Put(1, "user:42", []byte(`{"name":"ada","balance":100}`))
	cluster.StartRenewal(1 * time.Second)
	// Let background heartbeats renew the lease a few times first: lease
	// maintenance is amortized (one quorum round per leaseDuration/2),
	// never per-read.
	time.Sleep(1200 * time.Millisecond)

	// Phase 1: 10k concurrent reads inside the lease safe window.
	const readers = 10000
	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := cluster.Leader.Read(context.Background(), "user:42"); err != nil {
				panic(err)
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	local, degraded, readNet, renewals := cluster.Leader.Metrics.Snapshot()
	fmt.Println("\n--- Phase 1: lease inside absolute safe window ---")
	fmt.Printf("concurrent reads      : %d\n", readers)
	fmt.Printf("served locally        : %d (zero network overhead)\n", local)
	fmt.Printf("degraded reads        : %d\n", degraded)
	fmt.Printf("network rounds (reads): %d\n", readNet)
	fmt.Printf("total network rounds  : %d (background lease renewals x%d)\n", cluster.Leader.NetworkRounds(), renewals)
	fmt.Printf("elapsed               : %v\n", elapsed.Round(time.Microsecond))
	fmt.Printf("throughput            : %.0f reads/sec\n", float64(readers)/elapsed.Seconds())

	// Phase 2: stop heartbeats and wait until <100ms of lease remains.
	cluster.StopRenewal()
	for cluster.Leader.LeaseRemaining() >= leaseread.DefaultDegradeThreshold {
		time.Sleep(5 * time.Millisecond)
	}
	fmt.Println("\n--- Phase 2: lease remaining < 100ms, auto-degrade ---")
	fmt.Printf("lease remaining       : %v\n", cluster.Leader.LeaseRemaining().Round(time.Millisecond))

	before := cluster.Leader.NetworkRounds()
	v, err := cluster.Leader.Read(context.Background(), "user:42")
	if err != nil {
		panic(err)
	}
	after := cluster.Leader.NetworkRounds()
	fmt.Printf("read result           : %s\n", v)
	fmt.Printf("quorum heartbeats     : %d network rounds for this single read\n", after-before)
	fmt.Printf("lease renewed to      : %v remaining\n", cluster.Leader.LeaseRemaining().Round(time.Millisecond))

	// Phase 3: quorum lost -> strong consistency refuses to serve.
	cluster.Followers[0].SetDown(true)
	cluster.Followers[1].SetDown(true)
	for cluster.Leader.LeaseRemaining() >= leaseread.DefaultDegradeThreshold {
		time.Sleep(5 * time.Millisecond)
	}
	_, err = cluster.Leader.Read(context.Background(), "user:42")
	fmt.Println("\n--- Phase 3: quorum lost ---")
	fmt.Printf("read with no quorum   : err=%v (stale read rejected)\n", err)
}

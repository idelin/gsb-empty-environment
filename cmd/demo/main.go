// Command demo spins up a 50-node cluster from a single seed, then
// cold-starts 3 fresh nodes that know only the seed address, and
// measures bootstrap and gossip convergence times.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"seedmesh/discovery"
)

var transport = flag.String("transport", "mem",
	"node transport: \"mem\" (in-process, default) or \"tcp\" (real sockets)")

func mustStart(netw *discovery.MemNetwork, id string, seeds []string) *discovery.Node {
	cfg := discovery.Config{
		ID:             id,
		Addr:           "127.0.0.1:0",
		Seeds:          seeds,
		GossipInterval: 100 * time.Millisecond,
		SampleSize:     16,
	}
	if netw != nil {
		cfg.Listen = netw.Listen
		cfg.Dial = netw.Dial
	}
	n := discovery.NewNode(cfg)
	if err := n.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "start %s: %v\n", id, err)
		os.Exit(1)
	}
	return n
}

func waitFor(what string, timeout time.Duration, cond func() bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	fmt.Fprintf(os.Stderr, "timeout waiting for: %s\n", what)
	os.Exit(1)
}

func main() {
	flag.Parse()
	const clusterSize = 50
	const freshCount = 3
	total := clusterSize + freshCount

	var netw *discovery.MemNetwork
	if *transport == "mem" {
		netw = discovery.NewMemNetwork()
	}
	fmt.Printf("transport: %s\n", *transport)

	fmt.Printf("=== phase 0: forming %d-node cluster from a single seed ===\n", clusterSize)
	seed := mustStart(netw, "seed-0", nil)
	fmt.Printf("seed-0 listening on %s\n", seed.Addr())

	cluster := []*discovery.Node{seed}
	for i := 1; i < clusterSize; i++ {
		cluster = append(cluster, mustStart(netw, fmt.Sprintf("node-%02d", i), []string{seed.Addr()}))
	}
	waitFor("cluster formation", 10*time.Second, func() bool {
		return seed.MemberCount() == clusterSize
	})
	fmt.Printf("seed-0 tracks %d members\n\n", seed.MemberCount())

	fmt.Printf("=== phase 1: cold-starting %d fresh nodes (seed address only) ===\n", freshCount)
	var fresh []*discovery.Node
	start := time.Now()
	for i := 0; i < freshCount; i++ {
		f := mustStart(netw, fmt.Sprintf("fresh-%d", i), []string{seed.Addr()})
		fmt.Printf("fresh-%d bootstrap pulled %d members (cumulative %s)\n",
			i, f.MemberCount(), time.Since(start).Round(time.Millisecond))
		fresh = append(fresh, f)
	}
	elapsed := time.Since(start)
	fmt.Printf("bootstrap phase: %s (acceptance bound: 1s) -> %v\n\n",
		elapsed.Round(time.Millisecond), elapsed < time.Second)

	fmt.Println("=== phase 2: gossip shuffle ring convergence ===")
	start = time.Now()
	waitFor("gossip convergence", 10*time.Second, func() bool {
		for _, n := range cluster {
			if n.MemberCount() != total {
				return false
			}
		}
		for _, f := range fresh {
			if f.MemberCount() != total {
				return false
			}
		}
		return true
	})
	fmt.Printf("all %d nodes converged to %d members in %s\n",
		total, total, time.Since(start).Round(time.Millisecond))
	for _, f := range fresh {
		fmt.Printf("%s: %d members, %d successful gossip rounds\n",
			f.ID(), f.MemberCount(), f.GossipRounds())
	}

	for _, n := range cluster {
		n.Stop()
	}
	for _, n := range fresh {
		n.Stop()
	}
}

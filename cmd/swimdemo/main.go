// Command swimdemo runs a 4-node SWIM cluster on a real clock.
// The direct link A<->B is lossy, so A can only confirm B is alive
// through indirect PingReq proxies. Halfway through, node D is
// stopped to show real failure detection (Suspect -> Down).
package main

import (
	"fmt"
	"sort"
	"time"

	"swim"
)

func main() {
	clock := swim.RealClock{}
	net := swim.NewNetwork()
	events := make(chan swim.Event, 4096)

	ids := []string{"A", "B", "C", "D"}
	nodes := map[string]*swim.Node{}
	for i, id := range ids {
		var peers []string
		for _, other := range ids {
			if other != id {
				peers = append(peers, other)
			}
		}
		cfg := swim.Config{
			ID:              id,
			Interval:        500 * time.Millisecond,
			AckTimeout:      150 * time.Millisecond,
			IndirectTimeout: 300 * time.Millisecond,
			K:               2,
			Events:          events,
		}
		n := swim.NewNode(cfg, clock, net, peers, int64(i+1))
		net.Attach(id, n.Inbox())
		nodes[id] = n
		n.Start()
	}

	start := time.Now()
	go func() {
		for e := range events {
			fmt.Printf("  t=%5dms  %s\n", time.Since(start)/time.Millisecond, e)
		}
	}()

	fmt.Println("== weak network: direct link A<->B drops all packets ==")
	net.Drop("A", "B")
	net.Drop("B", "A")

	time.Sleep(3 * time.Second)
	fmt.Println("== node D stopped (simulating a crash) ==")
	nodes["D"].Stop()

	time.Sleep(6 * time.Second)

	fmt.Println("== final membership views ==")
	for _, id := range ids {
		m := nodes[id].Members()
		var keys []string
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fmt.Printf("  %s:", id)
		for _, k := range keys {
			fmt.Printf(" %s=%s", k, m[k])
		}
		fmt.Println()
	}

	for _, n := range nodes {
		n.Stop()
	}
}

package main

import (
	"fmt"
	"sync"
	"time"
)

func main() {
	gateway := NewGateway()
	node1 := &Node{ID: "Node-1"}
	node2 := &Node{ID: "Node-2"}
	gateway.AddNode(node1)
	gateway.AddNode(node2)

	// Initially the whole key space [A-Z) is leased to Node-1.
	if _, err := gateway.GrantLease(KeyRange{Start: "A", End: maxBound}, "Node-1"); err != nil {
		panic(err)
	}
	fmt.Println("== before split: single lease [A-Z) on Node-1 ==")
	for _, key := range []string{"Apple", "Zoo"} {
		lease, node, err := gateway.Route(key)
		if err != nil {
			panic(err)
		}
		fmt.Printf("  query %-6s -> range %-7s holder %s (epoch %d)\n",
			key, lease.Range, node.ID, lease.Epoch)
	}

	// The range grows too large: split [A-Z) at "M" and grant the two child
	// ranges to Node-1 and Node-2 independently.
	fmt.Println("\n== range [A-Z) too large, splitting at 'M' ==")
	left, right, err := gateway.Split("M", "Node-1", "Node-2")
	if err != nil {
		panic(err)
	}
	fmt.Printf("  granted %s to %s (epoch %d, inherited)\n", left.Range, left.NodeID, left.Epoch)
	fmt.Printf("  granted %s to %s (epoch %d, inherited)\n", right.Range, right.NodeID, right.Epoch)

	// Give each node 200ms of simulated work per query so concurrency is
	// visible: two concurrent queries should take ~200ms total, not ~400ms.
	node1.SetLatency(200 * time.Millisecond)
	node2.SetLatency(200 * time.Millisecond)

	fmt.Println("\n== concurrent queries after split ==")
	start := time.Now()
	var wg sync.WaitGroup
	for _, key := range []string{"Apple", "Zoo"} {
		wg.Add(1)
		go func(k string) {
			defer wg.Done()
			resp, err := gateway.Query(k)
			if err != nil {
				panic(err)
			}
			fmt.Printf("  query %-6s -> %s (took %v)\n", k, resp, time.Since(start).Round(time.Millisecond))
		}(key)
	}
	wg.Wait()
	elapsed := time.Since(start).Round(time.Millisecond)
	fmt.Printf("  both queries completed in %v (concurrent, not serialized)\n", elapsed)

	fmt.Println("\n== final lease table ==")
	for _, l := range gateway.Leases() {
		fmt.Printf("  %s\n", l.String())
	}
}

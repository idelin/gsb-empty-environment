// Command demo walks through the acceptance scenario end to end:
// the [A-Z] key space outgrows one node, splits at 'M', and the gateway
// grants the two child ranges to Node-1 and Node-2 as independent leases.
// Queries for "Apple" and "Zoo" are then served concurrently by the two
// nodes without blocking each other.
package main

import (
	"fmt"
	"sync"
	"time"

	"leaserouter"
)

const nodeLatency = 300 * time.Millisecond

func main() {
	router := leaserouter.NewRouter(30 * time.Second)
	node1 := leaserouter.NewNode("Node-1", nodeLatency)
	node2 := leaserouter.NewNode("Node-2", nodeLatency)
	router.AddNode(node1)
	router.AddNode(node2)

	// Seed data. After the split, Node-1 owns [A, M) and Node-2 owns
	// [M, ∞); the right-half rows are migrated to Node-2 as part of the
	// split (simulated here by pre-seeding).
	node1.Put("Apple", "fruit 🍎")
	node2.Put("Zoo", "place 🦒")

	gw := leaserouter.NewGateway(router)

	fmt.Println("== Step 1: whole key space [A-Z] leased to Node-1 ==")
	root, err := router.Grant(leaserouter.KeyRange{Start: "A"}, "Node-1")
	must(err)
	fmt.Println("  granted:", root)

	fmt.Println("\n== Step 2: range too large -> split at 'M', right half leased to Node-2 ==")
	left, right, err := router.Split("M", "Node-2")
	must(err)
	fmt.Println("  left  child (inherited by Node-1):", left)
	fmt.Println("  right child (granted to Node-2):  ", right)

	fmt.Println("\n== Step 3: concurrent queries 'Apple' and 'Zoo' ==")
	fmt.Printf("  (each node simulates %v processing latency)\n", nodeLatency)
	start := time.Now()
	var wg sync.WaitGroup
	for _, key := range []string{"Apple", "Zoo"} {
		wg.Add(1)
		go func(k string) {
			defer wg.Done()
			t0 := time.Now()
			value, err := gw.Query(k)
			must(err)
			lease, node, err := router.Route(k)
			must(err)
			fmt.Printf("  query %-6q -> %-7s via %-9s = %q (took %v)\n",
				k, lease.Range, node.ID, value, time.Since(t0).Round(time.Millisecond))
		}(key)
	}
	wg.Wait()
	elapsed := time.Since(start).Round(time.Millisecond)
	fmt.Printf("\nBoth queries finished in %v.\n", elapsed)
	fmt.Printf("Serial execution would take >= %v; the two nodes worked in parallel, unblocked.\n",
		2*nodeLatency)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

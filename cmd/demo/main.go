// Demo: Multi-Paxos consensus under network partition, plus
// locality-aware hierarchical shard routing.
package main

import (
	"fmt"

	"consensus/paxos"
	"consensus/shard"
)

func main() {
	demoPaxos()
	fmt.Println()
	demoSharding()
}

func demoPaxos() {
	fmt.Println("=== Multi-Paxos: 5-node cluster, partition & recovery ===")
	net := paxos.NewNetwork()
	nodes := make([]*paxos.Node, 5)
	for i := range nodes {
		nodes[i] = paxos.NewNode(i+1, net)
	}

	nodes[0].BecomeLeader()
	fmt.Println("[1] n1 elected leader (term round=1)")
	for _, v := range []string{"set x=1", "set y=2"} {
		if _, err := nodes[0].Propose(v); err != nil {
			panic(err)
		}
	}
	fmt.Println("[2] committed via lease (Phase 2 only): [set x=1, set y=2]")
	p1, p2 := nodes[0].Stats()
	fmt.Printf("    leader RPC rounds so far: phase1=%d phase2=%d\n", p1, p2)

	net.Partition([]int{1, 2}, []int{3, 4, 5})
	fmt.Println("[3] network partition: {n1,n2} | {n3,n4,n5}")
	if _, err := nodes[0].Propose("set z=dirty"); err != nil {
		fmt.Printf("    old leader n1 propose failed as expected: %v (dirty entry left on minority)\n", err)
	}

	if !nodes[2].BecomeLeader() {
		panic("n3 should win election on majority side")
	}
	fmt.Println("[4] n3 elected by majority (term round=2), Phase 1 repaired uncommitted slots")
	if _, err := nodes[2].Propose("set z=3"); err != nil {
		panic(err)
	}

	net.Heal()
	if _, err := nodes[2].Propose("set w=4"); err != nil {
		panic(err)
	}
	fmt.Println("[5] healed; anti-entropy sync overwrote minority dirty logs")
	for _, n := range nodes {
		fmt.Printf("    n%d applied: %s\n", n.ID(), applied(n))
	}
}

func applied(n *paxos.Node) string {
	out := "["
	for i, e := range n.Applied() {
		if i > 0 {
			out += ", "
		}
		if e.Noop {
			out += "<noop>"
		} else {
			out += e.Value
		}
	}
	return out + "]"
}

func demoSharding() {
	fmt.Println("=== Hierarchical sharding & locality-aware routing ===")
	const numShards, replicas = 1024, 3
	r := shard.NewRouter(numShards, replicas)

	id := 1
	var clients []shard.NodeInfo
	for z := 0; z < 3; z++ {
		for rk := 0; rk < 2; rk++ {
			for i := 0; i < 2; i++ {
				r.AddNode(shard.NodeInfo{
					ID:   id,
					Zone: fmt.Sprintf("zone-%c", 'a'+z),
					Rack: fmt.Sprintf("rack-%d", rk),
				})
				id++
			}
		}
		clients = append(clients, shard.NodeInfo{ID: 9000 + z, Zone: fmt.Sprintf("zone-%c", 'a'+z), Rack: "rack-0"})
	}
	fmt.Printf("[1] 12 nodes (3 zones x 2 racks x 2), %d virtual shards, RF=%d\n", numShards, replicas)

	dist := r.Distribution()
	min, max := 1<<30, 0
	for _, c := range dist {
		if c < min {
			min = c
		}
		if c > max {
			max = c
		}
	}
	fmt.Printf("[2] replica slots per node: min=%d max=%d (avg=%d)\n", min, max, numShards*replicas/12)

	// Locality: route 100k logical requests from clients in each zone.
	const requests = 100000
	var distCount [4]int
	for i := 0; i < requests; i++ {
		key := fmt.Sprintf("user:%d", i)
		client := clients[i%len(clients)]
		best, err := r.RouteLocal(key, client)
		if err != nil {
			panic(err)
		}
		distCount[shard.Distance(client, best)]++
	}
	local := distCount[0] + distCount[1] + distCount[2]
	fmt.Printf("[3] %d requests from zone-local clients:\n", requests)
	fmt.Printf("    same rack/zone (distance<=2): %d (%.1f%%)\n", local, 100*float64(local)/requests)
	fmt.Printf("    cross-zone  (distance=3):   %d (%.1f%%)\n", distCount[3], 100*float64(distCount[3])/requests)
	fmt.Printf("    -> random replica choice would send ~%.0f%% cross-zone\n",
		100*float64(2)/3)

	// Rebalance: add one node, measure shard movement.
	before := r.Snapshot()
	r.AddNode(shard.NodeInfo{ID: 100, Zone: "zone-a", Rack: "rack-9"})
	after := r.Snapshot()
	moved := 0
	for s := range before {
		if fmt.Sprint(before[s]) != fmt.Sprint(after[s]) {
			moved++
		}
	}
	fmt.Printf("[4] added 1 node: %d/%d shards moved (%.1f%%)\n", moved, numShards, 100*float64(moved)/numShards)
}

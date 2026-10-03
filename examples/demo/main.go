// Demo: 5-node Multi-Paxos cluster + locality-aware sharded routing.
//
// Scenario: leader commits entries, a network partition isolates it in the
// minority, the majority elects a new leader which repairs the log via
// Phase 1, and after healing the minority's dirty log is safely overwritten.
package main

import (
	"fmt"
	"time"

	"paxosshard/paxos"
	"paxosshard/shard"
)

const lease = 80 * time.Millisecond

func paxosDemo() {
	fmt.Println("=== Multi-Paxos: partition & leader replacement ===")
	net := paxos.NewNetwork()
	nodes := make([]*paxos.Node, 5)
	for i := range nodes {
		nodes[i] = paxos.NewNode(net, i, lease)
	}

	must(nodes[0].BecomeLeader())
	fmt.Println("node0 elected leader")
	for _, v := range []string{"set x=1", "set y=2", "set z=3"} {
		if _, err := nodes[0].Propose(v); err != nil {
			panic(err)
		}
	}
	fmt.Println("committed 3 entries cluster-wide")

	// Partition: {0,1} minority vs {2,3,4} majority.
	net.Cut([]int{0, 1}, []int{2, 3, 4})
	fmt.Println("network partitioned: {0,1} | {2,3,4}")

	// Old leader dirties slot 4 inside the minority (never committed).
	dirty := paxos.AcceptRequest{From: 0, Ballot: paxos.Ballot{Num: 1, Node: 0}, Slot: 4, Value: "dirty"}
	nodes[0].HandleAccept(dirty)
	nodes[1].HandleAccept(dirty)
	fmt.Println("old leader wrote uncommitted dirty value to minority slot 4")

	time.Sleep(lease + 40*time.Millisecond) // lease expires
	must(nodes[2].BecomeLeader())
	fmt.Println("node2 elected by majority; Phase 1 repaired the log")
	must2(nodes[2].Propose("set a=4"))
	must2(nodes[2].Propose("set b=5"))
	fmt.Println("new leader appended 2 entries under its lease (Phase 2 only)")

	net.Heal()
	if !nodes[2].Heartbeat() {
		panic("heartbeat failed")
	}
	must2(nodes[2].Propose("set c=6"))
	fmt.Println("partition healed; heartbeat converged the minority")

	for _, n := range nodes {
		fmt.Printf("  node%d committed log: %q\n", n.ID(), n.Applied())
	}
	if v, _ := nodes[1].SlotValue(4); v == "dirty" {
		panic("dirty value survived!")
	}
	fmt.Println("minority dirty slot 4 safely overwritten ✔")
}

func shardDemo() {
	fmt.Println("\n=== Hierarchical sharding & locality-aware routing ===")
	r, err := shard.NewRouter(1024, 3)
	must(err)
	id := 0
	for _, region := range []string{"cn-east", "cn-north", "us-west"} {
		for _, zone := range []string{"z1", "z2"} {
			for i := 0; i < 2; i++ {
				r.AddNode(&shard.Node{
					ID:       fmt.Sprintf("node-%d", id),
					Loc:      shard.Location{Region: region, Zone: zone},
					Capacity: 1,
				})
				id++
			}
		}
	}
	fmt.Printf("router: 12 nodes, 3 regions x 2 zones, %d vshards, rf=3\n", r.NumVShards())

	key := "user:42/profile"
	reps, _ := r.Replicas(key)
	fmt.Printf("key %q replicas: %v\n", key, reps)
	for _, client := range []shard.Location{
		{Region: "cn-east", Zone: "z1"},
		{Region: "us-west", Zone: "z2"},
	} {
		n, _ := r.RouteLocal(key, client)
		fmt.Printf("  client at %s/%s -> %s\n", client.Region, client.Zone, n)
	}

	var keys []string
	for i := 0; i < 5000; i++ {
		keys = append(keys, fmt.Sprintf("key-%d", i))
	}
	var clients []shard.Location
	for _, region := range []string{"cn-east", "cn-north", "us-west"} {
		for _, zone := range []string{"z1", "z2"} {
			clients = append(clients, shard.Location{Region: region, Zone: zone})
		}
	}
	st := r.Stats(keys, clients)
	fmt.Printf("avg routing distance: %.2f (1=same zone, 2=same region, 3=cross region)\n", st.AvgDistance)
	fmt.Printf("primary share per node: %d/%d keys (ideal %d)\n",
		st.PrimaryCount["node-0"], len(keys), len(keys)/12)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func must2(_ uint64, err error) { must(err) }

func main() {
	paxosDemo()
	shardDemo()
}

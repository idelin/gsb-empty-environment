// Command netsim runs three demos:
//  1. a healthy network: per-packet end-to-end latency,
//  2. a ring network that deadlocks: full deadlock evidence,
//  3. exhaustive state-space verification of small networks.
package main

import (
	"fmt"

	"netsim"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}

// diamond: 0 -> 1 -> 3 and 0 -> 2 -> 3, all traffic to 3 routed via 1.
func buildDiamond() (*netsim.Sim, []*netsim.Packet) {
	net := netsim.NewNetwork(4)
	for _, e := range [][2]int{{0, 1}, {0, 2}, {1, 3}, {2, 3}} {
		_, err := net.AddEdge(e[0], e[1], 1)
		must(err)
	}
	must(net.SetRoute(0, 3, 1))
	must(net.SetRoute(1, 3, 3))
	must(net.SetRoute(2, 3, 3))
	packets := []*netsim.Packet{
		{ID: 0, Src: 0, Dst: 3, InjectTick: 0},
		{ID: 1, Src: 0, Dst: 3, InjectTick: 1},
		{ID: 2, Src: 0, Dst: 3, InjectTick: 2},
		{ID: 3, Src: 2, Dst: 3, InjectTick: 0},
	}
	sim, err := netsim.NewSim(net, packets)
	must(err)
	return sim, packets
}

// ring: 0 -> 1 -> 2 -> 0, each node sends one packet across the ring.
func buildRing(packetCount int) (*netsim.Sim, []*netsim.Packet) {
	net := netsim.NewNetwork(3)
	for _, e := range [][2]int{{0, 1}, {1, 2}, {2, 0}} {
		_, err := net.AddEdge(e[0], e[1], 1)
		must(err)
	}
	// route toward dst via the next ring node
	must(net.SetRoute(0, 1, 1))
	must(net.SetRoute(0, 2, 1))
	must(net.SetRoute(1, 0, 2))
	must(net.SetRoute(1, 2, 2))
	must(net.SetRoute(2, 0, 0))
	must(net.SetRoute(2, 1, 0))
	var packets []*netsim.Packet
	for i := 0; i < packetCount; i++ {
		packets = append(packets, &netsim.Packet{
			ID:         i,
			Src:        i,
			Dst:        (i + 2) % 3, // two hops away
			InjectTick: 0,
		})
	}
	sim, err := netsim.NewSim(net, packets)
	must(err)
	return sim, packets
}

func demoLatency() {
	fmt.Println("== demo 1: healthy diamond network, per-packet latency ==")
	sim, packets := buildDiamond()
	res := sim.Run(sim.Initial(), 1000, nil)
	if !res.Done {
		fmt.Println("did not complete:", sim.DeadlockEvidence(res.Final))
		return
	}
	fmt.Printf("%-8s %-8s %-8s %-8s %-8s\n", "packet", "src->dst", "inject", "deliver", "latency")
	for _, p := range packets {
		lat, _ := sim.Latency(res.Final, p.ID)
		fmt.Printf("p%-7d %-8s %-8d %-8d %-8d\n", p.ID,
			fmt.Sprintf("%d->%d", p.Src, p.Dst), p.InjectTick, res.Final.Delivered[p.ID], lat)
	}
	fmt.Println()
}

func demoDeadlock() {
	fmt.Println("== demo 2: 3-node ring, capacity 1, one packet per node ==")
	sim, _ := buildRing(3)
	res := sim.Run(sim.Initial(), 1000, nil)
	if !res.Deadlock {
		fmt.Println("unexpectedly completed")
		return
	}
	fmt.Print(sim.DeadlockEvidence(res.Final))
	fmt.Println()
}

func demoEnumerate() {
	fmt.Println("== demo 3: exhaustive state-space verification ==")

	fmt.Println("-- ring with 2 packets (enough capacity) --")
	sim2, _ := buildRing(2)
	rep := sim2.Enumerate(100000)
	fmt.Printf("states=%d transitions=%d success-terminals=%d deadlock-terminals=%d invariant-violations=%d\n",
		rep.States, rep.Transitions, rep.SuccessTerminals, rep.DeadlockTerminals, len(rep.InvariantErrors))
	for id := range rep.MinLatency {
		fmt.Printf("  p%d latency: min=%d max=%d (over all schedules)\n", id, rep.MinLatency[id], rep.MaxLatency[id])
	}

	fmt.Println("-- ring with 3 packets (deadlock reachable) --")
	sim3, _ := buildRing(3)
	rep3 := sim3.Enumerate(100000)
	fmt.Printf("states=%d transitions=%d success-terminals=%d deadlock-terminals=%d invariant-violations=%d\n",
		rep3.States, rep3.Transitions, rep3.SuccessTerminals, rep3.DeadlockTerminals, len(rep3.InvariantErrors))
	if rep3.DeadlockExample != nil {
		fmt.Print(sim3.DeadlockEvidence(rep3.DeadlockExample))
	}
}

func main() {
	demoLatency()
	demoDeadlock()
	demoEnumerate()
}

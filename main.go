package main

import (
	"flag"
	"fmt"
)

// chainConfig builds a bidirectional chain 0-1-...-(n-1) with
// shortest-path routing and a configurable traffic set.
func chainConfig() Config {
	n := 4
	edges := []Edge{}
	next := make([][]int, n)
	for i := range next {
		next[i] = make([]int, n)
		for j := range next[i] {
			next[i][j] = -1
		}
	}
	for i := 0; i < n-1; i++ {
		edges = append(edges, Edge{i, i + 1, 1}, Edge{i + 1, i, 1})
	}
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if i == j {
				continue
			}
			if j > i {
				next[i][j] = i + 1
			} else {
				next[i][j] = i - 1
			}
		}
	}
	return Config{
		Nodes:     n,
		BufferCap: 4,
		Edges:     edges,
		NextHop:   next,
		Traffic: []Packet{
			{ID: 0, Src: 0, Dst: 3, Born: 0},
			{ID: 1, Src: 0, Dst: 3, Born: 0},
			{ID: 2, Src: 3, Dst: 0, Born: 1},
			{ID: 3, Src: 0, Dst: 2, Born: 2},
		},
	}
}

// ringConfig builds a unidirectional ring 0->1->...->(n-1)->0 with
// clockwise routing. With 2 packets per node, bufferCap=1 and edge
// cap=1 every slot fills and the ring deadlocks.
func ringConfig(n, packetsPerNode int) Config {
	edges := []Edge{}
	next := make([][]int, n)
	for i := range next {
		next[i] = make([]int, n)
		for j := range next[i] {
			next[i][j] = -1
		}
	}
	for i := 0; i < n; i++ {
		edges = append(edges, Edge{i, (i + 1) % n, 1})
	}
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if i != j {
				next[i][j] = (i + 1) % n
			}
		}
	}
	var traffic []Packet
	id := 0
	for i := 0; i < n; i++ {
		for k := 0; k < packetsPerNode; k++ {
			traffic = append(traffic, Packet{ID: id, Src: i, Dst: (i + 2) % n, Born: 0})
			id++
		}
	}
	return Config{Nodes: n, BufferCap: 1, Edges: edges, NextHop: next, Traffic: traffic}
}

// twoNodeConfig is the minimal deadlock-capable network: two nodes,
// one capacitated edge each way, packets crossing.
func twoNodeConfig(packetsEachWay int) Config {
	edges := []Edge{{0, 1, 1}, {1, 0, 1}}
	next := [][]int{{-1, 1}, {0, -1}}
	var traffic []Packet
	id := 0
	for k := 0; k < packetsEachWay; k++ {
		traffic = append(traffic, Packet{ID: id, Src: 0, Dst: 1, Born: 0})
		id++
		traffic = append(traffic, Packet{ID: id, Src: 1, Dst: 0, Born: 0})
		id++
	}
	return Config{Nodes: 2, BufferCap: 1, Edges: edges, NextHop: next, Traffic: traffic}
}

func runChainDemo() {
	fmt.Println("=== Scenario 1: 4-node chain, deterministic simulation ===")
	sim, err := NewSim(chainConfig())
	if err != nil {
		fmt.Println("config error:", err)
		return
	}
	res := sim.Run(1000)
	fmt.Print(sim.LatencyReport())
	fmt.Printf("finished at tick %d, deadlock=%v\n\n", res.Ticks, res.Deadlock)
}

func runRingDeadlockDemo() {
	fmt.Println("=== Scenario 2: 3-node ring, store-and-forward deadlock ===")
	sim, err := NewSim(ringConfig(3, 2))
	if err != nil {
		fmt.Println("config error:", err)
		return
	}
	res := sim.Run(1000)
	fmt.Print(sim.LatencyReport())
	if res.Deadlock {
		fmt.Printf("DEADLOCK detected at tick %d. Evidence:\n", res.Ticks)
		fmt.Print(FormatCycle(res.DeadlockE))
		fmt.Println("frozen state:")
		fmt.Print(sim.StateString())
	}
	fmt.Println()
}

func runEnumDemo() {
	fmt.Println("=== Scenario 3: exhaustive state enumeration (2-node network) ===")
	for _, k := range []int{1, 2} {
		cfg := twoNodeConfig(k)
		enum, err := NewEnumerator(cfg)
		if err != nil {
			fmt.Println("config error:", err)
			continue
		}
		res := enum.Run()
		fmt.Printf("packets=%d (each way %d): explored %d states, "+
			"terminal ok=%d, terminal deadlock=%d\n",
			2*k, k, res.States, res.TerminalOK, res.TerminalDead)
		if res.InvariantError != nil {
			fmt.Println("  INVARIANT VIOLATION:", res.InvariantError)
		}
		if res.DeadCycle != nil {
			fmt.Print("  " + FormatCycle(res.DeadCycle))
			last := res.DeadWitness[len(res.DeadWitness)-1]
			fmt.Printf("  deadlock reached after %d ticks; frozen state:\n", len(res.DeadWitness)-1)
			fmt.Print(enum.FormatState(last))
		}
	}
	fmt.Println()
}

func main() {
	scenario := flag.String("scenario", "all", "chain | ring | enum | all")
	flag.Parse()
	switch *scenario {
	case "chain":
		runChainDemo()
	case "ring":
		runRingDeadlockDemo()
	case "enum":
		runEnumDemo()
	case "all":
		runChainDemo()
		runRingDeadlockDemo()
		runEnumDemo()
	default:
		fmt.Println("unknown scenario:", *scenario)
	}
}

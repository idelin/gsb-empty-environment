// Command dvsim runs a sample distance-vector simulation: convergence on a
// 6-router topology, a link failure, recovery, and verification against
// reference shortest paths. Everything runs as discrete events on integer
// ticks; no real network traffic is generated.
package main

import (
	"fmt"

	"dvsim"
)

func check(net *dvsim.Network, phase string) bool {
	problems := dvsim.VerifyAgainstReference(net)
	if len(problems) == 0 {
		fmt.Printf("[%s] converged at tick %d: matches reference shortest paths\n",
			phase, net.ConvergedTick())
		return true
	}
	fmt.Printf("[%s] MISMATCH:\n", phase)
	for _, p := range problems {
		fmt.Println("  " + p)
	}
	return false
}

func main() {
	sim := dvsim.NewSimulator()
	net := dvsim.NewNetwork(sim, 42)

	// Topology:
	//   A --1-- B --1-- C
	//   |       |       |
	//   2       3       1
	//   |       |       |
	//   D --1-- E --1-- F
	for _, id := range []string{"A", "B", "C", "D", "E", "F"} {
		net.AddRouter(id)
	}
	net.AddLink("A", "B", 1)
	net.AddLink("B", "C", 1)
	net.AddLink("A", "D", 2)
	net.AddLink("B", "E", 3)
	net.AddLink("C", "F", 1)
	net.AddLink("D", "E", 1)
	net.AddLink("E", "F", 1)

	ok := true

	sim.Run(300)
	fmt.Println("=== phase 1: initial convergence ===")
	fmt.Print(net.DumpTables())
	ok = check(net, "phase 1") && ok

	fmt.Println("=== phase 2: link B-C goes down at tick", sim.Now(), "===")
	net.SetLink("B", "C", false)
	sim.Run(600)
	fmt.Print(net.DumpTables())
	ok = check(net, "phase 2") && ok

	fmt.Println("=== phase 3: link B-C restored at tick", sim.Now(), "===")
	net.SetLink("B", "C", true)
	sim.Run(900)
	ok = check(net, "phase 3") && ok

	fmt.Printf("messages: delivered=%d dropped=%d\n", net.Delivered, net.Dropped)
	maxSeen := 0
	for _, id := range net.RouterIDs() {
		if m := net.Router(id).MaxMetricSeen(); m > maxSeen {
			maxSeen = m
		}
	}
	fmt.Printf("max finite metric ever installed: %d (infinity=%d, count-to-infinity bounded)\n",
		maxSeen, dvsim.Infinity)

	if ok {
		fmt.Println("RESULT: PASS")
	} else {
		fmt.Println("RESULT: FAIL")
	}
}

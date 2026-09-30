// Command dvsim runs a sample distance-vector simulation: convergence on
// a 5-node topology, a link failure with rerouting, a node becoming
// unreachable, and link recovery. After every phase the routing tables
// are checked against reference shortest paths.
package main

import (
	"fmt"

	"dvsim"
)

func metric(m int) string {
	if m >= dvsim.Infinity {
		return "Inf"
	}
	return fmt.Sprintf("%d", m)
}

func printTables(sim *dvsim.Sim) {
	ids := sim.NodeIDs()
	for _, id := range ids {
		n := sim.Node(id)
		fmt.Printf("node %d routing table:\n", id)
		fmt.Printf("  %-5s %-8s %-8s %s\n", "dest", "metric", "nexthop", "version")
		for _, d := range ids {
			r, ok := n.Routes()[d]
			if !ok {
				fmt.Printf("  %-5d %-8s %-8s %s\n", d, "Inf", "-", "-")
				continue
			}
			fmt.Printf("  %-5d %-8s %-8d %d\n", d, metric(r.Metric), r.NextHop, r.Version)
		}
	}
}

func check(sim *dvsim.Sim) {
	ref := sim.ReferenceDistances()
	ok := true
	for _, src := range sim.NodeIDs() {
		for _, dst := range sim.NodeIDs() {
			want := dvsim.Infinity
			if d, reachable := ref[src][dst]; reachable && d < dvsim.Infinity {
				want = d
			}
			if got := sim.Node(src).Metric(dst); got != want {
				fmt.Printf("  MISMATCH node %d -> %d: got %s, want %s\n", src, dst, metric(got), metric(want))
				ok = false
			}
		}
	}
	if ok {
		fmt.Println("  check: tables match reference shortest paths")
	}
}

func phase(sim *dvsim.Sim, title string) {
	sim.Run(100000)
	fmt.Printf("== %s (converged at tick %d) ==\n", title, sim.Now)
	printTables(sim)
	check(sim)
	fmt.Println()
}

func main() {
	sim := dvsim.NewSim(42)
	for id := 0; id < 5; id++ {
		sim.AddNode(id)
	}
	sim.AddLink(0, 1, 1)
	sim.AddLink(1, 2, 1)
	l23 := sim.AddLink(2, 3, 1)
	l34 := sim.AddLink(3, 4, 1)
	l40 := sim.AddLink(4, 0, 4)
	sim.AddLink(1, 3, 2)

	sim.Start()
	phase(sim, "initial topology")

	fmt.Println("-- link 2-3 goes down --")
	sim.SetLinkUp(l23, false)
	phase(sim, "after link 2-3 failure")

	fmt.Println("-- links 3-4 and 4-0 go down: node 4 isolated --")
	sim.SetLinkUp(l34, false)
	sim.SetLinkUp(l40, false)
	phase(sim, "node 4 unreachable")

	fmt.Println("-- all links restored --")
	sim.SetLinkUp(l23, true)
	sim.SetLinkUp(l34, true)
	sim.SetLinkUp(l40, true)
	phase(sim, "after recovery")
}

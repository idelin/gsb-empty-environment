// Command demo runs a narrated scenario of the versioned majority
// register inside the single-process event simulator.
package main

import (
	"fmt"

	"majorityregister/register"
)

func main() {
	sim := register.NewSim(5, 42)
	sim.MinDelay, sim.MaxDelay = 1, 4
	fmt.Printf("cluster: n=%d nodes, quorum=%d, tolerates f=%d crashed nodes\n\n",
		sim.NumNodes(), sim.Quorum(), sim.FaultTolerance())

	alice := sim.NewClient()
	bob := sim.NewClient()
	carol := sim.NewClient()

	fmt.Println("[t=0] alice writes \"v1\"")
	w := alice.Write("v1")
	fmt.Printf("[t=%d] write completed: ok=%v tag=%+v\n\n", sim.Now, w.OK, w.Tag)

	fmt.Println("[t=now] bob and carol write concurrently (\"v2-bob\", \"v2-carol\")")
	wb := bob.WriteAsync("v2-bob")
	wc := carol.WriteAsync("v2-carol")
	sim.Run()
	fmt.Printf("[t=%d] bob:   ok=%v tag=%+v\n", sim.Now, wb.OK, wb.Tag)
	fmt.Printf("[t=%d] carol: ok=%v tag=%+v\n", sim.Now, wc.OK, wc.Tag)
	r := alice.Read()
	fmt.Printf("[t=%d] read resolves concurrent writes by unique tag -> %q (tag=%+v)\n\n",
		sim.Now, r.Result, r.Tag)

	fmt.Println("[t=now] crash 2 of 5 nodes (f failures tolerated)")
	sim.Nodes()[1].Crash()
	sim.Nodes()[3].Crash()
	r = alice.Read()
	fmt.Printf("[t=%d] read with f=2 down: ok=%v value=%q\n\n", sim.Now, r.OK, r.Result)

	fmt.Println("[t=now] crash a 3rd node: no majority available")
	sim.Nodes()[2].Crash()
	w = alice.Write("v3")
	fmt.Printf("[t=%d] write without majority: done=%v ok=%v (not completed, no false success)\n\n",
		sim.Now, w.Done, w.OK)

	fmt.Println("[t=now] restart all nodes (state survives on simulated disk)")
	for _, n := range sim.Nodes() {
		n.Restart()
	}
	r = alice.Read()
	fmt.Printf("[t=%d] read after restart: ok=%v value=%q tag=%+v\n", sim.Now, r.OK, r.Result, r.Tag)

	fmt.Println("\nper-node durable state at the end:")
	for _, n := range sim.Nodes() {
		tag, v, stored := n.Stored()
		fmt.Printf("  node %d: up=%v stored=%v tag=%+v value=%q\n", n.ID(), n.Up(), stored, tag, v)
	}
}

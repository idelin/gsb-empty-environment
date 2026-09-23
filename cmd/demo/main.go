// Command demo runs a scripted reconfiguration: a 3-node old set hands
// over to a disjoint 3-node new set through the joint-majority stage,
// while the joint-stage leader crashes mid-switch and recovers.
package main

import (
	"fmt"

	"jointpaxos"
)

func main() {
	oldSet := []string{"a", "b", "c"}
	newSet := []string{"x", "y", "z"}

	joint := jointpaxos.Config{Old: oldSet, New: newSet, Joint: true}
	final := jointpaxos.Config{Old: newSet}

	propose := func(view int) jointpaxos.Value {
		switch view {
		case 0:
			return jointpaxos.Value{Cmd: "op-1", Next: &joint}
		case 1:
			return jointpaxos.Value{Cmd: "op-2", Next: &final}
		default:
			return jointpaxos.Value{Cmd: "op-3"}
		}
	}

	sim := &jointpaxos.Sim{Logger: func(s string) { fmt.Println(s) }}
	net := jointpaxos.NewNetwork(sim)

	nodes := map[string]*jointpaxos.Node{}
	stores := map[string]*jointpaxos.Store{}
	for _, id := range append(append([]string{}, oldSet...), newSet...) {
		stores[id] = jointpaxos.NewStore(jointpaxos.Config{Old: oldSet})
		nodes[id] = jointpaxos.NewNode(id, stores[id], sim, net, propose)
	}
	for _, nd := range nodes {
		nd.Boot()
	}

	// Fault script, driven by the simulator's virtual clock:
	// crash the joint-stage leader "a" and "x" mid-switch, restart later.
	sim.After(6, func() {
		nodes["a"].Crash()
		nodes["x"].Crash()
	})
	sim.After(40, func() {
		nodes["a"].Restart()
		nodes["x"].Restart()
	})

	sim.Run(200)

	fmt.Println("\n=== final state ===")
	for _, id := range []string{"a", "b", "c", "x", "y", "z"} {
		nd := nodes[id]
		fmt.Printf("%s: view=%d commits=%v stale-ignored=%d rejected-reconfig=%d violations=%v\n",
			id, nd.CurrentView(), nd.CommittedCmds(), nd.StaleIgnoredCount(), nd.RejectedReconfigs(), nd.ViolationList())
	}
}

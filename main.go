// Command swimlab-demo runs the deterministic SWIM race scenario and prints
// the full event timeline, the final cluster state, and the run digest.
package main

import (
	"fmt"

	"swimlab/scenario"
)

func main() {
	const seed = 42
	const steps = 10000

	s, nodes := scenario.BuildRace(seed)
	s.Run(steps)

	fmt.Printf("SWIM deterministic race demo: seed=%d steps=%d\n", seed, steps)
	fmt.Printf("network: sent=%d dropped=%d\n\n", s.Switch().Sent, s.Switch().Dropped)

	fmt.Println("event timeline:")
	for _, line := range s.Log {
		fmt.Println("  " + line)
	}

	fmt.Println("\nfinal state:")
	for _, id := range []string{"A", "B", "C"} {
		n := nodes[id]
		fmt.Printf("  node %s: incarnation=%d refutations=%d declared-dead=%v\n",
			id, n.Incarnation(), len(n.Refutations), n.DeclaredDead)
		for _, peer := range []string{"A", "B", "C"} {
			if peer == id {
				continue
			}
			state, inc, _ := n.MemberState(peer)
			fmt.Printf("    sees %s: %s (inc=%d)\n", peer, state, inc)
		}
	}
	fmt.Printf("\nevent log digest: %s\n", s.Digest())
}

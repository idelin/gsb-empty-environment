// Command demo simulates two sub-clusters diverging under a network
// partition and re-aligning after the heal via 4-byte CRC digest
// exchanges and interval reconciliation.
package main

import (
	"fmt"

	"gsb"
)

func main() {
	const numNodes = 6
	res := gsb.SimulatePartitionHeal(42, numNodes, 5)

	fmt.Printf("partition healed: %d nodes probe peers and swap 4-byte CRC digests\n", numNodes)
	for round, divergent := range res.DivergentPerRound {
		fmt.Printf("round %d: %d divergent exchange(s) reconciled\n", round+1, divergent)
	}
	if !res.Converged {
		fmt.Println("FAILED: views did not converge within 5 rounds")
		return
	}
	fmt.Printf("converged in %d round(s); final view on every node:\n", res.Rounds)
	for _, n := range res.Cluster.Nodes {
		fmt.Printf("  %s digest=%08x members:", n.ID, n.DigestSum())
		for _, m := range n.Members() {
			fmt.Printf(" %s=%s/%d", m.ID, m.State, m.Incarnation)
		}
		fmt.Println()
	}
}

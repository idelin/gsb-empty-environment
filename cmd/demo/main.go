// Command demo simulates four weighted nodes voting through a network
// partition and merging to a deterministic decision after the heal.
package main

import (
	"fmt"

	decision "weightedvote"
)

func main() {
	// Four nodes agree on the initial stake configuration (total 100).
	alpha := decision.NewSelector("alpha")
	beta := decision.NewSelector("beta")
	gamma := decision.NewSelector("gamma")
	delta := decision.NewSelector("delta")
	for _, r := range []*decision.Selector{alpha, beta, gamma, delta} {
		r.SetWeight("alpha", 30)
		r.SetWeight("beta", 20)
		r.SetWeight("gamma", 25)
		r.SetWeight("delta", 25)
	}
	fmt.Println("=== initial configuration (all replicas in sync) ===")
	fmt.Println(alpha)

	fmt.Println("\n=== network partition: {alpha, beta} | {gamma, delta} ===")
	// Side 1: votes plus a stake deposit for alpha (30 -> 40).
	alpha.CastVote(decision.Yes)
	beta.CastVote(decision.No)
	alpha.SetWeight("alpha", 40)
	alpha.Merge(beta)
	beta.Merge(alpha)
	fmt.Println("-- side 1 after intra-partition sync --")
	fmt.Println(alpha)

	// Side 2: gamma abstains, delta votes yes, delta is slashed (25 -> 10).
	gamma.CastVote(decision.Abstain)
	delta.CastVote(decision.Yes)
	gamma.SetWeight("delta", 10)
	gamma.Merge(delta)
	delta.Merge(gamma)
	fmt.Println("-- side 2 after intra-partition sync --")
	fmt.Println(gamma)

	fmt.Println("\n=== partition heals: full state exchange ===")
	for _, r := range []*decision.Selector{alpha, beta, gamma, delta} {
		for _, other := range []*decision.Selector{alpha, beta, gamma, delta} {
			r.Merge(other)
		}
	}
	fmt.Println(alpha)

	fmt.Println("\n=== converged tallies (identical on every replica) ===")
	for _, r := range []*decision.Selector{alpha, beta, gamma, delta} {
		fmt.Printf("%-6s %s\n", r.ID()+":", r.Tally())
	}
	fmt.Printf("\nfinal decision: %s\n", alpha.Decision())
}

package gsb

import (
	"fmt"
	"math/rand"
)

// ScenarioResult reports the outcome of a partition/heal simulation.
type ScenarioResult struct {
	Cluster           *Cluster
	Rounds            int   // probe rounds needed to fully align after heal
	DivergentPerRound []int // divergent exchanges observed in each round
	Converged         bool
}

// SimulatePartitionHeal reproduces the acceptance scenario:
//
//  1. numNodes nodes start with a consistent, fully-alive view.
//  2. The network partitions into two halves; each half declares the
//     other half's members dead (the views diverge).
//  3. The partition heals; every node refutes its own dead mark by
//     bumping its incarnation and re-announcing itself alive.
//  4. Probe rounds run: peers swap 4-byte digest CRCs, and on mismatch
//     reconcile only the divergent hash-ring intervals.
//
// It stops as soon as every node's view is identical, or maxRounds is hit.
func SimulatePartitionHeal(seed int64, numNodes, maxRounds int) *ScenarioResult {
	ids := make([]string, numNodes)
	for i := range ids {
		ids[i] = fmt.Sprintf("node-%d", i)
	}
	c := NewCluster(ids...)

	// Steady state before the partition: everyone knows everyone, alive.
	for _, n := range c.Nodes {
		for _, id := range ids {
			n.Upsert(Member{ID: id, State: Alive, Incarnation: 1})
		}
	}

	// Partition: each half marks the other half's members dead.
	half := numNodes / 2
	c.Partition(func(id string) int {
		if id <= ids[half-1] {
			return 0
		}
		return 1
	})
	for _, n := range c.Nodes {
		myGroup := c.group[n.ID]
		for _, id := range ids {
			if c.group[id] != myGroup {
				n.Upsert(Member{ID: id, State: Dead, Incarnation: 1})
			}
		}
	}

	// Heal: each node refutes the dead rumor about itself.
	c.Heal()
	for _, n := range c.Nodes {
		n.Upsert(Member{ID: n.ID, State: Alive, Incarnation: 2})
	}

	// Probe rounds until the views fully align.
	rng := rand.New(rand.NewSource(seed))
	res := &ScenarioResult{Cluster: c}
	for round := 1; round <= maxRounds; round++ {
		res.DivergentPerRound = append(res.DivergentPerRound, c.Round(rng))
		if c.Converged() {
			res.Rounds = round
			res.Converged = true
			break
		}
	}
	return res
}

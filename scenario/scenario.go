// Package scenario wires up deterministic SWIM test scenarios on top of the
// discrete-event simulator. It is shared by the go test suite and the demo
// binary so both exercise exactly the same code path.
package scenario

import (
	"math/rand"

	"swimlab/sim"
	"swimlab/swim"
)

// RaceConfig is the protocol timing used by the race scenario (in ticks).
var RaceConfig = swim.Config{
	Period:         10,
	AckTimeout:     5,
	SuspectTimeout: 20,
}

// Timeline of the engineered race (with RaceConfig):
//
//	tick 100: link A<->B is partitioned (100% loss both directions)
//	tick 105: A suspects B and B suspects A (acked pings stop flowing)
//	tick 124: link A<->B heals
//	tick 125: A declares B dead and B declares A dead IN THE SAME TICK;
//	          both death declarations cross the healed link and each node
//	          must arbitrate its own death claim
const (
	PartitionTick = 100
	HealTick      = 124
	DeadTick      = 125
)

// BuildRace creates the 3-node mutual-death-declaration race scenario.
// Node C stays fully connected and acts as a gossip witness.
func BuildRace(seed int64) (*sim.Simulator, map[string]*swim.Node) {
	rng := rand.New(rand.NewSource(seed))
	sw := sim.NewSwitch(rng, 0.0, 1, 1) // no background loss, 1-2 tick delay
	s := sim.NewSimulator(sw)

	nodes := make(map[string]*swim.Node)
	ids := []string{"A", "B", "C"}
	for _, id := range ids {
		nodes[id] = swim.NewNode(id, s, RaceConfig)
	}
	for _, id := range ids {
		for _, peer := range ids {
			if peer != id {
				nodes[id].AddMember(peer)
			}
		}
		s.AddNode(nodes[id])
	}

	s.Schedule(PartitionTick, func() {
		sw.SetLinkLoss("A", "B", 1.0)
		sw.SetLinkLoss("B", "A", 1.0)
		s.Logf("TEST: partition link A<->B (loss=1.0)")
	})
	s.Schedule(HealTick, func() {
		sw.SetLinkLoss("A", "B", 0.0)
		sw.SetLinkLoss("B", "A", 0.0)
		s.Logf("TEST: heal link A<->B (loss=0.0)")
	})
	return s, nodes
}

// BuildChaos creates a 5-node scenario with background packet loss and
// latency jitter and no engineered partition. Used to prove determinism
// under adverse network conditions.
func BuildChaos(seed int64, loss float64, jitter uint64) (*sim.Simulator, map[string]*swim.Node) {
	rng := rand.New(rand.NewSource(seed))
	sw := sim.NewSwitch(rng, loss, 1, jitter)
	s := sim.NewSimulator(sw)

	nodes := make(map[string]*swim.Node)
	ids := []string{"A", "B", "C", "D", "E"}
	for _, id := range ids {
		nodes[id] = swim.NewNode(id, s, swim.Config{
			Period:         10,
			AckTimeout:     8,
			SuspectTimeout: 40,
		})
	}
	for _, id := range ids {
		for _, peer := range ids {
			if peer != id {
				nodes[id].AddMember(peer)
			}
		}
		s.AddNode(nodes[id])
	}
	return s, nodes
}

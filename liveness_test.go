package syncsim

import (
	"fmt"
	"math/rand"
	"testing"
)

// LIVENESS: under message loss, duplication, reordering and delay,
// repeated synchronisation rounds reach a fixpoint and all replicas
// converge to identical state (tombstones included).
func TestFixpointUnderFaults(t *testing.T) {
	s := NewSim(42, nil)
	s.DropRate = 0.20
	s.DupRate = 0.15
	s.MinDelay, s.MaxDelay = 1, 20 // reordering via random delays
	nodes := addNodes(s, "A", "B", "C", "D")

	// Random concurrent workload: puts and deletes on all nodes.
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 200; i++ {
		n := nodes[rng.Intn(len(nodes))]
		k := fmt.Sprintf("k%03d", rng.Intn(60))
		if rng.Intn(4) == 0 {
			n.Store.Delete(k)
		} else {
			n.Store.Put(k, fmt.Sprintf("v%d", i))
		}
	}

	rounds, ok := runUntilFixpoint(s, nodes, 60)
	if !ok {
		t.Fatalf("no fixpoint within 60 rounds (last round %d)", rounds)
	}
	mustConverge(t, nodes)
	t.Logf("fixpoint after %d rounds; sent=%d dropped=%d duplicated=%d applied=%d",
		rounds, s.Sent, s.Dropped, s.Duplicated, totalApplied(nodes))

	// Fixpoint evidence: one more full round changes nothing.
	before := totalApplied(nodes)
	syncRound(s, nodes)
	if totalApplied(nodes) != before {
		t.Fatal("state changed after fixpoint")
	}
}

// LIVENESS: a crashed node that restarts catches up and the system
// still converges; messages to a crashed node are lost, not corrupting.
func TestCrashRestart(t *testing.T) {
	s := NewSim(9, nil)
	s.DropRate = 0.1
	nodes := addNodes(s, "A", "B", "C")

	nodes[0].Store.Put("x", "1")
	s.Crash("C")
	// C misses these rounds entirely.
	nodes[0].Store.Put("y", "2")
	nodes[1].Store.Delete("x")
	syncRound(s, nodes)
	if _, ok := nodes[2].Store.LiveValue("y"); ok {
		t.Fatal("crashed node should not receive updates")
	}

	s.Restart("C")
	// More churn while C rejoins.
	nodes[1].Store.Put("z", "3")
	rounds, ok := runUntilFixpoint(s, nodes, 60)
	if !ok {
		t.Fatalf("no convergence after restart (round %d)", rounds)
	}
	mustConverge(t, nodes)
	if _, ok := nodes[2].Store.LiveValue("x"); ok {
		t.Fatal("delete of x did not reach restarted node")
	}
	if v, _ := nodes[2].Store.LiveValue("y"); v != "2" {
		t.Fatal("restarted node missed put of y")
	}
}

// CONTROL: communication, faults and time are fully determined by the
// simulator seed — two runs with the same seed are bit-identical.
func TestDeterministicSimulation(t *testing.T) {
	run := func() (int, int, int, map[string]Record) {
		s := NewSim(1234, nil)
		s.DropRate = 0.25
		s.DupRate = 0.2
		s.MinDelay, s.MaxDelay = 1, 30
		nodes := addNodes(s, "A", "B", "C")
		rng := rand.New(rand.NewSource(3))
		for i := 0; i < 100; i++ {
			n := nodes[rng.Intn(3)]
			n.Store.Put(fmt.Sprintf("k%02d", rng.Intn(20)), fmt.Sprintf("v%d", i))
		}
		if _, ok := runUntilFixpoint(s, nodes, 60); !ok {
			t.Fatal("no fixpoint")
		}
		return s.Sent, s.Dropped, s.Duplicated, nodes[0].Store.Records()
	}
	sent1, drop1, dup1, state1 := run()
	sent2, drop2, dup2, state2 := run()
	if sent1 != sent2 || drop1 != drop2 || dup1 != dup2 {
		t.Fatalf("fault injection not deterministic: (%d,%d,%d) vs (%d,%d,%d)",
			sent1, drop1, dup1, sent2, drop2, dup2)
	}
	if len(state1) != len(state2) {
		t.Fatal("final states differ in size")
	}
	for k, v1 := range state1 {
		if v2, ok := state2[k]; !ok || v1 != v2 {
			t.Fatalf("final states differ at %q", k)
		}
	}
}

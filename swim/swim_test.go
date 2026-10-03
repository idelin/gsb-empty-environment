package swim_test

import (
	"testing"

	"swimlab/scenario"
	"swimlab/swim"
)

const (
	raceSeed  = int64(42)
	raceSteps = uint64(10000)
)

// TestMutualDeadDeclarationRace engineers the extreme race in which nodes A
// and B declare each other dead in the very same tick, then verifies that
// the arbitration code resolves it deterministically: both nodes refute
// their own death, bump their incarnation, and the cluster converges back
// to all-alive. The full 10000-tick run is pinned to a golden digest so the
// scenario is reproduced bit-for-bit on every run.
func TestMutualDeadDeclarationRace(t *testing.T) {
	s, nodes := scenario.BuildRace(raceSeed)
	s.Run(raceSteps)

	a, b, c := nodes["A"], nodes["B"], nodes["C"]

	// 1. The race must have happened: A and B declared each other dead in
	//    exactly the same tick.
	if got := a.DeclaredDead["B"]; got != scenario.DeadTick {
		t.Fatalf("A declared B dead at tick %d, want %d", got, scenario.DeadTick)
	}
	if got := b.DeclaredDead["A"]; got != scenario.DeadTick {
		t.Fatalf("B declared A dead at tick %d, want %d", got, scenario.DeadTick)
	}

	// 2. Arbitration: each node refuted its own death exactly once.
	if got := len(a.Refutations); got != 1 {
		t.Fatalf("A refuted %d times, want 1", got)
	}
	if got := len(b.Refutations); got != 1 {
		t.Fatalf("B refuted %d times, want 1", got)
	}
	if a.Refutations[0] != b.Refutations[0] {
		t.Fatalf("refutations not simultaneous: A at %d, B at %d",
			a.Refutations[0], b.Refutations[0])
	}

	// 3. Final cluster state: everyone alive, incarnations settled.
	for id, n := range nodes {
		if got := n.Incarnation(); got != 1 && id != "C" {
			t.Fatalf("node %s incarnation = %d, want 1", id, got)
		}
	}
	if got := c.Incarnation(); got != 0 {
		t.Fatalf("witness C incarnation = %d, want 0 (never accused)", got)
	}
	for _, observer := range nodes {
		for _, peer := range []string{"A", "B", "C"} {
			if observer.ID() == peer {
				continue
			}
			state, _, _ := observer.MemberState(peer)
			if state != swim.Alive {
				t.Fatalf("node %s sees %s as %s, want alive", observer.ID(), peer, state)
			}
		}
	}

	// 4. Golden digest: the entire 10000-tick execution is pinned.
	const golden = "3f0f60683db75f63"
	if got := s.Digest(); got != golden {
		t.Fatalf("event log digest = %s, want golden %s", got, golden)
	}
}

// TestDeterministicReplay runs the race scenario twice with the same seed
// and requires byte-identical event logs: the race is 100% reproducible.
func TestDeterministicReplay(t *testing.T) {
	s1, _ := scenario.BuildRace(raceSeed)
	s1.Run(raceSteps)
	s2, _ := scenario.BuildRace(raceSeed)
	s2.Run(raceSteps)

	if s1.Digest() != s2.Digest() {
		t.Fatalf("digests differ across replays: %s vs %s", s1.Digest(), s2.Digest())
	}
	if len(s1.Log) != len(s2.Log) {
		t.Fatalf("log lengths differ: %d vs %d", len(s1.Log), len(s2.Log))
	}
	for i := range s1.Log {
		if s1.Log[i] != s2.Log[i] {
			t.Fatalf("log line %d differs:\n  %q\n  %q", i, s1.Log[i], s2.Log[i])
		}
	}
}

// TestChaosDeterminism hammers a 5-node cluster for 10000 ticks with 20%
// packet loss and heavy jitter, twice, and requires identical outcomes.
func TestChaosDeterminism(t *testing.T) {
	const seed = int64(7)
	s1, n1 := scenario.BuildChaos(seed, 0.20, 3)
	s1.Run(raceSteps)
	s2, n2 := scenario.BuildChaos(seed, 0.20, 3)
	s2.Run(raceSteps)

	if s1.Digest() != s2.Digest() {
		t.Fatalf("chaos run not deterministic: %s vs %s", s1.Digest(), s2.Digest())
	}
	for id := range n1 {
		if n1[id].Incarnation() != n2[id].Incarnation() {
			t.Fatalf("node %s incarnation diverged: %d vs %d",
				id, n1[id].Incarnation(), n2[id].Incarnation())
		}
	}
	if s1.Switch().Dropped == 0 {
		t.Fatal("expected packet drops in chaos scenario")
	}
	t.Logf("chaos: sent=%d dropped=%d digest=%s",
		s1.Switch().Sent, s1.Switch().Dropped, s1.Digest())
}

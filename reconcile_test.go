package gsb

import (
	"math/rand"
	"testing"
)

// Acceptance: two sub-clusters diverge under a partition; at the healing
// instant, 4-byte CRC digest exchanges detect the inconsistency and the
// cluster fully aligns both ways within 5 probe rounds.
func TestPartitionHealConvergesWithinFiveRounds(t *testing.T) {
	res := SimulatePartitionHeal(42, 6, 5)
	if !res.Converged {
		t.Fatalf("views did not converge within 5 rounds (divergent per round: %v)", res.DivergentPerRound)
	}
	if res.Rounds == 0 || res.Rounds > 5 {
		t.Fatalf("converged in %d rounds, want 1..5", res.Rounds)
	}
	t.Logf("converged in %d rounds; divergent exchanges per round: %v", res.Rounds, res.DivergentPerRound)

	// Full bidirectional alignment: every node sees every member alive
	// with the post-heal incarnation.
	for _, n := range res.Cluster.Nodes {
		members := n.Members()
		if len(members) != 6 {
			t.Fatalf("%s holds %d members, want 6", n.ID, len(members))
		}
		for _, m := range members {
			if m.State != Alive || m.Incarnation != 2 {
				t.Fatalf("%s sees %s as %v inc=%d, want alive inc=2", n.ID, m.ID, m.State, m.Incarnation)
			}
		}
	}
}

// The scenario must converge for a spread of probe-order seeds, not just
// one lucky run.
func TestPartitionHealConvergesAcrossSeeds(t *testing.T) {
	for seed := int64(0); seed < 50; seed++ {
		res := SimulatePartitionHeal(seed, 6, 5)
		if !res.Converged {
			t.Fatalf("seed %d: no convergence within 5 rounds (divergent: %v)", seed, res.DivergentPerRound)
		}
	}
}

// The 4-byte CRC digest detects divergence and stays quiet when views match.
func TestDigestSumDetectsMismatch(t *testing.T) {
	a, b := NewNode("a"), NewNode("b")
	for _, id := range []string{"a", "b", "c"} {
		m := Member{ID: id, State: Alive, Incarnation: 1}
		a.Upsert(m)
		b.Upsert(m)
	}
	if a.DigestSum() != b.DigestSum() {
		t.Fatal("identical views produced different digest sums")
	}
	b.Upsert(Member{ID: "c", State: Dead, Incarnation: 2})
	if a.DigestSum() == b.DigestSum() {
		t.Fatal("divergent views produced equal digest sums")
	}
}

// A single divergent member must only light up its own ring interval, so
// reconciliation pulls a bounded slice instead of the full member list.
func TestDiffSegmentsLocalizesDivergence(t *testing.T) {
	a, b := NewNode("a"), NewNode("b")
	shared := []string{"m0", "m1", "m2", "m3", "m4", "m5", "m6", "m7"}
	for _, id := range shared {
		m := Member{ID: id, State: Alive, Incarnation: 1}
		a.Upsert(m)
		b.Upsert(m)
	}
	Sync(a, b) // align self-entries so only the injected divergence remains
	b.Upsert(Member{ID: "extra", State: Alive, Incarnation: 1})

	diff := a.digest.DiffSegments(&b.digest)
	want := int(segmentIndex("extra"))
	if len(diff) != 1 || diff[0] != want {
		t.Fatalf("diff segments = %v, want [%d]", diff, want)
	}

	// Sync must converge both nodes after pulling just that interval.
	if !Sync(a, b) {
		t.Fatal("Sync reported no divergence")
	}
	if a.DigestSum() != b.DigestSum() {
		t.Fatal("digests still differ after Sync")
	}
	if _, ok := a.Get("extra"); !ok {
		t.Fatal("member 'extra' was not pulled to node a")
	}
}

// Incremental add/remove maintenance must match a digest rebuilt from
// scratch over the final member list.
func TestIncrementalDigestMatchesRebuild(t *testing.T) {
	n := NewNode("n")
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 200; i++ {
		id := string(rune('a' + rng.Intn(26)))
		n.Upsert(Member{ID: id, State: State(rng.Intn(3)), Incarnation: uint64(rng.Intn(10))})
	}
	var fresh Digest
	for _, m := range n.Members() {
		fresh.Add(m)
	}
	if n.digest != fresh {
		t.Fatal("incremental digest diverged from a from-scratch rebuild")
	}
}

// Merge conflict resolution: higher incarnation wins; ties go to the more
// severe state.
func TestMergeConflictResolution(t *testing.T) {
	n := NewNode("n")
	n.Upsert(Member{ID: "x", State: Alive, Incarnation: 5})
	if n.Merge([]Member{{ID: "x", State: Dead, Incarnation: 4}}) != 0 {
		t.Fatal("stale incarnation must not overwrite a newer entry")
	}
	if n.Merge([]Member{{ID: "x", State: Dead, Incarnation: 5}}) != 1 {
		t.Fatal("same-incarnation dead mark must override alive")
	}
	if m, _ := n.Get("x"); m.State != Dead {
		t.Fatalf("x is %v, want dead", m.State)
	}
	if n.Merge([]Member{{ID: "x", State: Alive, Incarnation: 6}}) != 1 {
		t.Fatal("newer incarnation must refute the dead mark")
	}
	if m, _ := n.Get("x"); m.State != Alive {
		t.Fatalf("x is %v, want alive after refutation", m.State)
	}
}

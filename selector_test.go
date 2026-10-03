package decision

import (
	"fmt"
	"testing"
)

// mergeAll folds every replica into a fresh selector, simulating full
// state exchange after a network heal.
func mergeAll(id string, replicas ...*Selector) *Selector {
	m := NewSelector(id)
	for _, r := range replicas {
		m.Merge(r)
	}
	return m
}

// TestPartitionedVotingAndHeal is the acceptance scenario: four nodes vote
// under a network partition while weights change dynamically; after the
// heal the merge must compute the correct total effective weight, abstain
// ballots, and a deterministic decision.
func TestPartitionedVotingAndHeal(t *testing.T) {
	// Initial configuration, agreed by all four nodes before the split:
	// alpha=30, beta=20, gamma=25, delta=25 (total 100).
	alpha := NewSelector("alpha")
	beta := NewSelector("beta")
	gamma := NewSelector("gamma")
	delta := NewSelector("delta")
	for _, r := range []*Selector{alpha, beta, gamma, delta} {
		r.SetWeight("alpha", 30)
		r.SetWeight("beta", 20)
		r.SetWeight("gamma", 25)
		r.SetWeight("delta", 25)
	}

	// --- Network partition: {alpha, beta} | {gamma, delta} ---

	// Side 1: alpha votes YES, beta votes NO, and alpha's stake is
	// raised 30 -> 40 (e.g. a deposit). The side syncs internally.
	alpha.CastVote(Yes)
	beta.CastVote(No)
	alpha.SetWeight("alpha", 40)
	alpha.Merge(beta)
	beta.Merge(alpha)

	// Side 2: gamma abstains, delta votes YES, and delta's stake is
	// slashed 25 -> 10. The side syncs internally.
	gamma.CastVote(Abstain)
	delta.CastVote(Yes)
	gamma.SetWeight("delta", 10)
	gamma.Merge(delta)
	delta.Merge(gamma)

	// --- Heal: every replica exchanges state with every other. ---
	healed := mergeAll("healed", alpha, beta, gamma, delta)
	tally := healed.Tally()

	// Effective total weight: 40 + 20 + 25 + 10 = 95.
	if tally.TotalWeight != 95 {
		t.Errorf("TotalWeight = %d, want 95", tally.TotalWeight)
	}
	if tally.YesWeight != 50 || tally.YesCount != 2 {
		t.Errorf("Yes = %d(%d), want 50(2)", tally.YesWeight, tally.YesCount)
	}
	if tally.NoWeight != 20 || tally.NoCount != 1 {
		t.Errorf("No = %d(%d), want 20(1)", tally.NoWeight, tally.NoCount)
	}
	if tally.AbstainWeight != 25 || tally.AbstainCount != 1 {
		t.Errorf("Abstain = %d(%d), want 25(1)", tally.AbstainWeight, tally.AbstainCount)
	}
	if len(tally.MissingVoters) != 0 {
		t.Errorf("MissingVoters = %v, want none", tally.MissingVoters)
	}
	// YES holds 50 of 95: a strict majority -> APPROVED.
	if got := tally.Decision(); got != Approved {
		t.Errorf("Decision = %s, want APPROVED", got)
	}

	// Every healed replica must independently reach the same state.
	for _, r := range []*Selector{alpha, beta, gamma, delta} {
		r.Merge(healed)
		if got := r.Decision(); got != Approved {
			t.Errorf("replica %s decision = %s, want APPROVED", r.ID(), got)
		}
		if got := r.Tally().TotalWeight; got != 95 {
			t.Errorf("replica %s total = %d, want 95", r.ID(), got)
		}
	}
}

// TestMergeOrderIndependence verifies commutativity/associativity: every
// permutation of merge order yields the identical tally and decision.
func TestMergeOrderIndependence(t *testing.T) {
	build := func() []*Selector {
		names := []string{"n1", "n2", "n3", "n4"}
		rs := make([]*Selector, len(names))
		for i, n := range names {
			rs[i] = NewSelector(n)
			rs[i].SetWeight("n1", 10)
			rs[i].SetWeight("n2", 20)
			rs[i].SetWeight("n3", 30)
			rs[i].SetWeight("n4", 40)
		}
		rs[0].CastVote(Yes)
		rs[1].CastVote(No)
		rs[2].CastVote(Abstain)
		rs[3].CastVote(Yes)
		rs[0].SetWeight("n2", 5)  // concurrent weight change
		rs[2].SetWeight("n2", 15) // conflicting concurrent change
		return rs
	}

	want := mergeAll("ref", build()...).Tally()

	perm := []int{0, 1, 2, 3}
	checked := 0
	var permute func(int)
	permute = func(i int) {
		if i == len(perm) {
			rs := build()
			m := NewSelector("m")
			for _, idx := range perm {
				m.Merge(rs[idx])
			}
			got := m.Tally()
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Errorf("order %v: tally %s, want %s", perm, got, want)
			}
			checked++
			return
		}
		for j := i; j < len(perm); j++ {
			perm[i], perm[j] = perm[j], perm[i]
			permute(i + 1)
			perm[i], perm[j] = perm[j], perm[i]
		}
	}
	permute(0)
	if checked != 24 {
		t.Fatalf("checked %d permutations, want 24", checked)
	}
}

// TestMergeIdempotent ensures re-merging the same state changes nothing.
func TestMergeIdempotent(t *testing.T) {
	a := NewSelector("a")
	a.SetWeight("a", 7)
	a.CastVote(Yes)
	b := NewSelector("b")
	b.Merge(a)
	first := fmt.Sprint(b.Tally())
	b.Merge(a)
	b.Merge(a.Clone())
	if got := fmt.Sprint(b.Tally()); got != first {
		t.Errorf("idempotency violated: %s != %s", got, first)
	}
}

// TestCausalWeightUpdate: a stale weight must never override a newer one,
// regardless of merge direction.
func TestCausalWeightUpdate(t *testing.T) {
	old := NewSelector("old")
	old.SetWeight("x", 100)

	newer := NewSelector("new")
	newer.Merge(old)         // learns weight=100 at t=1
	newer.SetWeight("x", 42) // newer causal update at t=2

	a := mergeAll("a", old, newer)
	b := mergeAll("b", newer, old)
	for name, m := range map[string]*Selector{"old,new": a, "new,old": b} {
		w, _ := m.WeightOf("x")
		if w.Value != 42 {
			t.Errorf("merge order %s: weight = %d, want 42 (stale update won)", name, w.Value)
		}
	}
}

// TestConcurrentWeightConflict: two partitions update the same node's
// weight concurrently; the winner is deterministic for any merge order.
func TestConcurrentWeightConflict(t *testing.T) {
	build := func() (*Selector, *Selector) {
		p := NewSelector("p")
		q := NewSelector("q")
		p.SetWeight("x", 10)
		q.SetWeight("x", 20)
		return p, q
	}
	p1, q1 := build()
	fwd := mergeAll("f", p1, q1)
	p2, q2 := build()
	rev := mergeAll("r", q2, p2)

	wf, _ := fwd.WeightOf("x")
	wr, _ := rev.WeightOf("x")
	if wf != wr {
		t.Errorf("non-deterministic conflict resolution: %+v vs %+v", wf, wr)
	}
	// Lamport times tie at 1; source "q" > "p", so q's update wins.
	if wf.Value != 20 || wf.Source != "q" {
		t.Errorf("winner = %+v, want value=20 source=q", wf)
	}
}

// TestVoteChange: a node may recast its ballot; the latest causal vote wins.
func TestVoteChange(t *testing.T) {
	a := NewSelector("a")
	a.SetWeight("a", 10)
	a.CastVote(Yes)
	a.CastVote(No) // voter changes mind

	other := NewSelector("b")
	other.Merge(a)
	a.Merge(other) // echo back; must not resurrect the old ballot

	v, _ := a.VoteOf("a")
	if v.Choice != No {
		t.Errorf("vote = %s, want NO after recast", v.Choice)
	}
	tally := a.Tally()
	if tally.NoWeight != 10 || tally.YesWeight != 0 {
		t.Errorf("tally = %s, want no=10 yes=0", tally)
	}
}

// TestDecisionThresholds pins the strict-majority rule, including the
// effect of abstentions on the effective total.
func TestDecisionThresholds(t *testing.T) {
	cases := []struct {
		name                   string
		yes, no, abstain, miss uint64 // weights per camp
		want                   Decision
	}{
		{"yes strict majority", 51, 49, 0, 0, Approved},
		{"yes exact half", 50, 50, 0, 0, Undecided},
		{"no strict majority", 10, 60, 0, 0, Rejected},
		{"abstain blocks majority", 50, 0, 50, 0, Undecided},
		{"abstain counted in total", 51, 0, 49, 0, Approved},
		{"missing voter blocks majority", 50, 0, 0, 50, Undecided},
		{"empty", 0, 0, 0, 0, Undecided},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewSelector("test")
			add := func(name string, w uint64, c Choice, voted bool) {
				s.SetWeight(name, w)
				if voted {
					s.votes[name] = Vote{Choice: c, Time: s.tick(), Voter: name}
				}
			}
			if tc.yes > 0 {
				add("yes", tc.yes, Yes, true)
			}
			if tc.no > 0 {
				add("no", tc.no, No, true)
			}
			if tc.abstain > 0 {
				add("abstain", tc.abstain, Abstain, true)
			}
			if tc.miss > 0 {
				add("miss", tc.miss, Abstain, false)
			}
			if got := s.Decision(); got != tc.want {
				t.Errorf("Decision = %s, want %s (tally: %s)", got, tc.want, s.Tally())
			}
		})
	}
}

// TestZeroWeightVoteIgnored: ballots from zero-weight nodes do not count.
func TestZeroWeightVoteIgnored(t *testing.T) {
	s := NewSelector("z")
	s.SetWeight("z", 0)
	s.CastVote(Yes)
	tally := s.Tally()
	if tally.YesWeight != 0 || tally.YesCount != 0 || tally.VotedWeight != 0 {
		t.Errorf("zero-weight vote counted: %s", tally)
	}
	if tally.Decision() != Undecided {
		t.Errorf("Decision = %s, want UNDECIDED", tally.Decision())
	}
}

// TestDeterministicString ensures state rendering is stable across replicas.
func TestDeterministicString(t *testing.T) {
	a := NewSelector("a")
	a.SetWeight("x", 1)
	a.SetWeight("y", 2)
	a.CastVote(Yes)
	b := NewSelector("b")
	b.Merge(a)
	if a.String() == "" || b.String() == "" {
		t.Fatal("empty state rendering")
	}
	// Same merged content modulo replica id/clock line.
	ta, tb := a.Tally(), b.Tally()
	if fmt.Sprint(ta) != fmt.Sprint(tb) {
		t.Errorf("tallies diverge: %s vs %s", ta, tb)
	}
}

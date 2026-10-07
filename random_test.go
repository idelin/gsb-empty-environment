package plansel

import (
	"math/rand"
	"sort"
	"testing"
	"time"
)

// Cross-check the branch-and-bound solver against exhaustive enumeration on
// random instances, both without and with committed state.
func TestRandomCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(20261007))
	for trial := 0; trial < 300; trial++ {
		n := 1 + rng.Intn(12)
		inst := Instance{GroupFee: map[int]int64{}}
		for i := 0; i < n; i++ {
			g := rng.Intn(3)
			p := Plan{
				ID:     i + 1,
				Group:  g,
				Cost:   int64(rng.Intn(15)),
				Profit: int64(rng.Intn(31) - 5), // sometimes negative
			}
			// Requirements only point to earlier plans: acyclic by construction.
			for j := 0; j < i; j++ {
				if rng.Intn(6) == 0 {
					p.Requires = append(p.Requires, j+1)
				}
			}
			if _, ok := inst.GroupFee[g]; !ok || rng.Intn(2) == 0 {
				inst.GroupFee[g] = int64(rng.Intn(12))
			}
			inst.Plans = append(inst.Plans, p)
		}
		total := int64(0)
		for _, p := range inst.Plans {
			total += p.Cost
		}
		for _, f := range inst.GroupFee {
			total += f
		}
		inst.Budget = total * int64(rng.Intn(101)) / 100

		sess, err := NewSession(inst)
		if err != nil {
			t.Fatalf("trial %d: NewSession: %v", trial, err)
		}
		// Randomly commit a dependency-closed seed set on half the trials.
		committed := map[int]bool{}
		prepared := map[int]bool{}
		if rng.Intn(2) == 0 {
			var seed []int
			for _, p := range inst.Plans {
				if rng.Intn(4) == 0 {
					seed = append(seed, p.ID)
				}
			}
			if err := sess.Commit(seed...); err != nil {
				t.Fatalf("trial %d: Commit: %v", trial, err)
			}
			if rng.Intn(2) == 0 {
				if err := sess.Prepare(rng.Intn(3)); err != nil {
					t.Fatalf("trial %d: Prepare: %v", trial, err)
				}
			}
			for _, id := range sess.Committed() {
				committed[id] = true
			}
			for _, g := range sess.Prepared() {
				prepared[g] = true
			}
		}

		res, err := sess.Solve(noDeadline)
		if err != nil {
			t.Fatalf("trial %d: Solve: %v", trial, err)
		}
		if res.Status != Optimal {
			t.Fatalf("trial %d: status %v, want Optimal", trial, res.Status)
		}
		if err := Verify(sess.Instance(), sess.View(), res); err != nil {
			t.Fatalf("trial %d: Verify: %v", trial, err)
		}

		// Direct comparison against exhaustive enumeration.
		bfProfit, bfCost, bfIDs := bruteForce(inst, committed, prepared)
		base := int64(0)
		for _, p := range inst.Plans {
			if committed[p.ID] {
				base += p.Profit
			}
		}
		if res.Profit != base+bfProfit {
			t.Fatalf("trial %d: profit %d, brute force %d", trial, res.Profit, base+bfProfit)
		}
		if res.Cost != bfCost {
			t.Fatalf("trial %d: cost %d, brute force %d", trial, res.Cost, bfCost)
		}
		want := append([]int(nil), sess.Committed()...)
		want = append(want, bfIDs...)
		sort.Ints(want)
		if !equalIDs(res.Selected, want) {
			t.Fatalf("trial %d: Selected %v, brute force %v", trial, res.Selected, want)
		}
	}
}

// A deadline-interrupted random search always resumes to the same optimum
// as an uninterrupted one.
func TestRandomResumeMatchesOneShot(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 30; trial++ {
		inst := Instance{Budget: 60, GroupFee: map[int]int64{0: 4, 1: 6, 2: 2}}
		for i := 0; i < 18; i++ {
			p := Plan{
				ID:     i + 1,
				Group:  rng.Intn(3),
				Cost:   int64(1 + rng.Intn(12)),
				Profit: int64(rng.Intn(20)),
			}
			if i > 0 && rng.Intn(5) == 0 {
				p.Requires = []int{rng.Intn(i) + 1}
			}
			inst.Plans = append(inst.Plans, p)
		}
		sess, err := NewSession(inst)
		if err != nil {
			t.Fatal(err)
		}
		want, err := sess.Solve(noDeadline)
		if err != nil {
			t.Fatal(err)
		}

		sess2, err := NewSession(inst)
		if err != nil {
			t.Fatal(err)
		}
		r, err := sess2.Solve(pastDeadline())
		if err != nil {
			t.Fatal(err)
		}
		for r.Status != Optimal {
			r, err = sess2.Resume(r.ResumeToken(), pastDeadline())
			if err != nil {
				t.Fatal(err)
			}
		}
		if r.Profit != want.Profit || r.Cost != want.Cost || !equalIDs(r.Selected, want.Selected) {
			t.Fatalf("trial %d: resumed %v/%d/%d, one-shot %v/%d/%d",
				trial, r.Selected, r.Profit, r.Cost, want.Selected, want.Profit, want.Cost)
		}
		if err := Verify(inst, View{}, r); err != nil {
			t.Fatalf("trial %d: Verify: %v", trial, err)
		}
	}
}

func pastDeadline() time.Time {
	return time.Now().Add(-time.Second)
}

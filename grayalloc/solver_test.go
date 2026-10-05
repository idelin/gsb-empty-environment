package main

import (
	"math/rand"
	"testing"
)

// brute is the reference exponential solver used to cross-check Solve.
func brute(groups []Group, lo, hi int64) (best *Assignment, reachable map[int64]bool) {
	n := len(groups)
	reachable = map[int64]bool{}
	for m := 0; m < 1<<uint(n); m++ {
		a := &Assignment{OnNew: make([]bool, n)}
		for i := 0; i < n; i++ {
			onNew := (uint(m)>>uint(i))&1 == 1
			a.OnNew[i] = onNew
			if onNew {
				a.NewUsers += groups[i].Size
			}
			if onNew != groups[i].OnNew {
				a.MovedUsers += groups[i].Size
				a.MovedGroups++
			}
		}
		reachable[a.NewUsers] = true
		if a.NewUsers < lo || a.NewUsers > hi {
			continue
		}
		if best == nil ||
			a.MovedUsers < best.MovedUsers ||
			(a.MovedUsers == best.MovedUsers && a.MovedGroups < best.MovedGroups) {
			cpy := *a
			best = &cpy
		}
	}
	return best, reachable
}

func TestHandComputed(t *testing.T) {
	// Current new-version users: 100 (g2). Target interval [300, 300].
	// Whole-group options hitting 300: g1+g3 (moves 300 users, 2 groups)
	// or g2+g3 (keeps g2, moves only g3: 200 users, 1 group) — unique optimum.
	groups := []Group{
		{ID: "g1", Size: 100, OnNew: false},
		{ID: "g2", Size: 100, OnNew: true},
		{ID: "g3", Size: 200, OnNew: false},
		{ID: "g4", Size: 150, OnNew: false},
	}
	asg, proof, err := Solve(groups, 300, 300)
	if err != nil || proof != nil {
		t.Fatalf("expected feasible, got proof=%v err=%v", proof, err)
	}
	if asg.NewUsers != 300 || asg.MovedUsers != 200 || asg.MovedGroups != 1 {
		t.Fatalf("got %+v", asg)
	}
	want := []bool{false, true, true, false}
	for i := range want {
		if asg.OnNew[i] != want[i] {
			t.Fatalf("assignment %v, want %v", asg.OnNew, want)
		}
	}
}

func TestInfeasibleProof(t *testing.T) {
	// Sizes 10, 20, 40: reachable sums are multiples of 10 up to 70.
	// Interval [33, 37] contains none; gap certificate is 30 and 40.
	groups := []Group{
		{ID: "a", Size: 10},
		{ID: "b", Size: 20},
		{ID: "c", Size: 40},
	}
	asg, proof, err := Solve(groups, 33, 37)
	if err != nil {
		t.Fatal(err)
	}
	if asg != nil || proof == nil {
		t.Fatalf("expected infeasible, got %+v", asg)
	}
	if proof.Below != 30 || proof.Above != 40 {
		t.Fatalf("proof = %+v", proof)
	}
	if proof.GCD != 10 {
		t.Fatalf("gcd = %d", proof.GCD)
	}
	t.Log(proof.Describe())
}

func TestProofNeverSplitsGroups(t *testing.T) {
	// One group of 100 can never satisfy [1, 99]; proof must report the
	// honest gap (0 and 100) instead of pretending a split is possible.
	asg, proof, err := Solve([]Group{{ID: "only", Size: 100}}, 1, 99)
	if err != nil {
		t.Fatal(err)
	}
	if asg != nil {
		t.Fatal("must not split the group")
	}
	if proof.Below != 0 || proof.Above != 100 {
		t.Fatalf("proof = %+v", proof)
	}
}

func TestCrossCheckBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 60; trial++ {
		n := 1 + rng.Intn(MaxGroups)
		groups := make([]Group, n)
		var total int64
		for i := range groups {
			groups[i] = Group{
				ID:    string(rune('a'+i%26)) + string(rune('0'+i/26)),
				Size:  1 + int64(rng.Intn(500)),
				OnNew: rng.Intn(2) == 0,
			}
			total += groups[i].Size
		}
		lo := int64(rng.Intn(int(total) + 1))
		hi := lo + int64(rng.Intn(int(total-lo)+1))

		asg, proof, err := Solve(groups, lo, hi)
		if err != nil {
			t.Fatal(err)
		}
		want, reachable := brute(groups, lo, hi)

		if want == nil {
			if proof == nil {
				t.Fatalf("trial %d: Solve claims feasible, brute disagrees", trial)
			}
			// Verify the certificate against the true reachable set.
			if proof.Below >= 0 && (!reachable[proof.Below] || proof.Below >= lo) {
				t.Fatalf("trial %d: bad Below %d", trial, proof.Below)
			}
			if proof.Above >= 0 && (!reachable[proof.Above] || proof.Above <= hi) {
				t.Fatalf("trial %d: bad Above %d", trial, proof.Above)
			}
			for s := range reachable {
				if s > proof.Below && s < lo {
					t.Fatalf("trial %d: %d reachable, closer than Below=%d", trial, s, proof.Below)
				}
				if s < proof.Above && s > hi {
					t.Fatalf("trial %d: %d reachable, closer than Above=%d", trial, s, proof.Above)
				}
			}
			continue
		}
		if asg == nil {
			t.Fatalf("trial %d: Solve claims infeasible, brute found %+v", trial, want)
		}
		if asg.NewUsers < lo || asg.NewUsers > hi {
			t.Fatalf("trial %d: %d users outside [%d,%d]", trial, asg.NewUsers, lo, hi)
		}
		if asg.MovedUsers != want.MovedUsers || asg.MovedGroups != want.MovedGroups {
			t.Fatalf("trial %d: got (%d users, %d groups), optimal is (%d, %d)",
				trial, asg.MovedUsers, asg.MovedGroups, want.MovedUsers, want.MovedGroups)
		}
		// The reported plan must be consistent with the decisions.
		var newUsers, movedUsers int64
		var movedGroups int
		for i, g := range groups {
			if asg.OnNew[i] {
				newUsers += g.Size
			}
			if asg.OnNew[i] != g.OnNew {
				movedUsers += g.Size
				movedGroups++
			}
		}
		if newUsers != asg.NewUsers || movedUsers != asg.MovedUsers || movedGroups != asg.MovedGroups {
			t.Fatalf("trial %d: inconsistent plan %+v", trial, asg)
		}
	}
}

func TestValidation(t *testing.T) {
	tooMany := make([]Group, MaxGroups+1)
	for i := range tooMany {
		tooMany[i] = Group{ID: string(rune('a' + i)), Size: 1}
	}
	if _, _, err := Solve(tooMany, 0, 1); err == nil {
		t.Fatal("expected error for >20 groups")
	}
	if _, _, err := Solve([]Group{{ID: "x", Size: 0}}, 0, 1); err == nil {
		t.Fatal("expected error for zero size")
	}
	if _, _, err := Solve([]Group{{ID: "x", Size: 1}}, 5, 2); err == nil {
		t.Fatal("expected error for empty interval")
	}
}

func TestIntervalFromRatio(t *testing.T) {
	ratio, tol := 0.10, 0.005
	minNew, maxNew := int64(0), int64(0)
	in := Input{Ratio: &ratio, Tolerance: &tol}
	lo, hi, err := interval(in, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if lo != 950 || hi != 1050 {
		t.Fatalf("got [%d, %d]", lo, hi)
	}
	// Explicit interval overrides ratio.
	in.MinNew, in.MaxNew = &minNew, &maxNew
	if lo, hi, _ = interval(in, 10000); lo != 0 || hi != 0 {
		t.Fatalf("explicit interval ignored: [%d, %d]", lo, hi)
	}
}

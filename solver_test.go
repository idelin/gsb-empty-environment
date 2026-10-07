package sched

import (
	"fmt"
	"math/rand"
	"testing"
)

// bruteForce enumerates all n! permutations and returns the best one by
// the documented objective: (max lateness, total setup, lexicographic
// order of indices). It is an independent reference implementation.
func bruteForce(orders []Order, setup [][]int, initialType int) Schedule {
	n := len(orders)
	perm := make([]int, n)
	for i := range perm {
		perm[i] = i
	}
	var best []int
	bestKey := []int{0, 0} // maxLateness, totalSetup of best
	keyOf := func(p []int) (int, int) {
		now, totalSetup, maxLat := 0, 0, 0
		prevType := initialType
		for i, idx := range p {
			o := orders[idx]
			if setup != nil && o.Type != prevType {
				totalSetup += setup[prevType][o.Type]
			}
			now += o.Duration
			if lat := now - o.Deadline; i == 0 || lat > maxLat {
				maxLat = lat
			}
			prevType = o.Type
		}
		return maxLat, totalSetup
	}
	less := func(p []int, maxLat, totalSetup int) bool {
		if best == nil {
			return true
		}
		if maxLat != bestKey[0] {
			return maxLat < bestKey[0]
		}
		if totalSetup != bestKey[1] {
			return totalSetup < bestKey[1]
		}
		for i := range p {
			if p[i] != best[i] {
				return p[i] < best[i]
			}
		}
		return false
	}
	var rec func(k int)
	rec = func(k int) {
		if k == n {
			maxLat, totalSetup := keyOf(perm)
			if less(perm, maxLat, totalSetup) {
				best = append([]int{}, perm...)
				bestKey = []int{maxLat, totalSetup}
			}
			return
		}
		for i := k; i < n; i++ {
			perm[k], perm[i] = perm[i], perm[k]
			rec(k + 1)
			perm[k], perm[i] = perm[i], perm[k]
		}
	}
	rec(0)
	return Evaluate(orders, setup, initialType, best)
}

func indices(s Schedule) []int {
	out := make([]int, len(s.Entries))
	for i, e := range s.Entries {
		out[i] = e.Index
	}
	return out
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestSolveMatchesBruteForce cross-checks Solve against full permutation
// enumeration on random small instances with asymmetric setup matrices
// and narrow value ranges to force frequent ties.
func TestSolveMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 200; trial++ {
		n := 1 + rng.Intn(7) // 1..7 orders
		nTypes := 1 + rng.Intn(3)
		orders := make([]Order, n)
		for i := range orders {
			orders[i] = Order{
				ID:       fmt.Sprintf("J%d", i),
				Type:     rng.Intn(nTypes),
				Duration: 1 + rng.Intn(5),
				Deadline: rng.Intn(16),
			}
		}
		setup := make([][]int, nTypes)
		for a := range setup {
			setup[a] = make([]int, nTypes)
			for b := range setup[a] {
				setup[a][b] = rng.Intn(6) // asymmetric in general
			}
		}
		initialType := rng.Intn(nTypes)

		got := Solve(orders, setup, initialType)
		want := bruteForce(orders, setup, initialType)

		if got.MaxLateness != want.MaxLateness ||
			got.TotalSetup != want.TotalSetup ||
			!equalInts(indices(got), indices(want)) {
			t.Fatalf("trial %d mismatch:\n orders=%+v\n setup=%v initialType=%d\n got  %v (maxLat=%d setup=%d)\n want %v (maxLat=%d setup=%d)",
				trial, orders, setup, initialType,
				indices(got), got.MaxLateness, got.TotalSetup,
				indices(want), want.MaxLateness, want.TotalSetup)
		}
	}
}

// TestAsymmetricSetup builds an instance where the optimal direction is
// decided purely by the asymmetry of the changeover matrix, then flips
// the matrix and expects the flipped order.
func TestAsymmetricSetup(t *testing.T) {
	orders := []Order{
		{ID: "A", Type: 1, Duration: 4, Deadline: 20},
		{ID: "B", Type: 2, Duration: 4, Deadline: 20},
	}
	// initial type 0; going 0->1->2 costs 1+1=2, going 0->2->1 costs 2+9=11.
	setup := [][]int{
		{0, 1, 2},
		{9, 0, 1},
		{1, 9, 0},
	}
	got := Solve(orders, setup, 0)
	if want := []int{0, 1}; !equalInts(indices(got), want) || got.TotalSetup != 2 {
		t.Fatalf("got seq=%v setup=%d, want seq=%v setup=2", indices(got), got.TotalSetup, want)
	}

	// Transposed matrix: the cheap direction reverses.
	transposed := [][]int{
		{0, 9, 1},
		{1, 0, 9},
		{2, 1, 0},
	}
	got = Solve(orders, transposed, 0)
	if want := []int{1, 0}; !equalInts(indices(got), want) || got.TotalSetup != 2 {
		t.Fatalf("transposed: got seq=%v setup=%d, want seq=%v setup=2", indices(got), got.TotalSetup, want)
	}
}

// TestTieBreakLexicographic: several permutations share the optimal
// (maxLateness, totalSetup); the lexicographically smallest index
// sequence must win.
func TestTieBreakLexicographic(t *testing.T) {
	orders := []Order{
		{ID: "J0", Type: 1, Duration: 3, Deadline: 30},
		{ID: "J1", Type: 1, Duration: 3, Deadline: 30},
		{ID: "J2", Type: 2, Duration: 3, Deadline: 30},
	}
	// Every type change costs 3, so [J0 J1 J2] and [J1 J0 J2] both pay 6
	// (one change into type 1, one into type 2); all other orders pay 9.
	setup := [][]int{
		{0, 3, 3},
		{3, 0, 3},
		{3, 3, 0},
	}
	got := Solve(orders, setup, 0)
	if want := []int{0, 1, 2}; !equalInts(indices(got), want) || got.TotalSetup != 6 {
		t.Fatalf("got seq=%v setup=%d, want seq=%v setup=6", indices(got), got.TotalSetup, want)
	}

	// Fully symmetric instance: every permutation ties, identity wins.
	same := []Order{
		{ID: "A", Type: 0, Duration: 2, Deadline: 10},
		{ID: "B", Type: 0, Duration: 2, Deadline: 10},
		{ID: "C", Type: 0, Duration: 2, Deadline: 10},
	}
	got = Solve(same, nil, 0)
	if want := []int{0, 1, 2}; !equalInts(indices(got), want) {
		t.Fatalf("got seq=%v, want %v", indices(got), want)
	}
}

// TestMaxLatenessDominatesSetup: a cheaper-setup order must lose when it
// worsens the maximum lateness.
func TestMaxLatenessDominatesSetup(t *testing.T) {
	orders := []Order{
		{ID: "urgent", Type: 1, Duration: 2, Deadline: 2},
		{ID: "relaxed", Type: 2, Duration: 5, Deadline: 100},
	}
	// Initial type 2: scheduling "relaxed" first would avoid all setup
	// cost, but "urgent" would then finish at 7 >> deadline 2.
	setup := [][]int{
		{0, 5, 5},
		{5, 0, 5},
		{5, 5, 0},
	}
	got := Solve(orders, setup, 2)
	if want := []int{0, 1}; !equalInts(indices(got), want) {
		t.Fatalf("got seq=%v, want %v", indices(got), want)
	}
	if got.MaxLateness != 0 || got.TotalSetup != 10 {
		t.Fatalf("got maxLat=%d setup=%d, want 0 and 10", got.MaxLateness, got.TotalSetup)
	}
}

// TestInitialTypeCharged: the first order pays changeover from the
// initial machine type only when the types differ.
func TestInitialTypeCharged(t *testing.T) {
	orders := []Order{{ID: "A", Type: 1, Duration: 3, Deadline: 10}}
	setup := [][]int{
		{0, 7},
		{4, 0},
	}
	if got := Solve(orders, setup, 0); got.TotalSetup != 7 {
		t.Fatalf("initial type 0: got setup=%d, want 7", got.TotalSetup)
	}
	if got := Solve(orders, setup, 1); got.TotalSetup != 0 {
		t.Fatalf("initial type 1: got setup=%d, want 0", got.TotalSetup)
	}
}

func TestEmptyAndSingle(t *testing.T) {
	got := Solve(nil, nil, 0)
	if len(got.Entries) != 0 || got.MaxLateness != 0 || got.TotalSetup != 0 {
		t.Fatalf("empty: got %+v", got)
	}
	orders := []Order{{ID: "only", Type: 0, Duration: 5, Deadline: 3}}
	got = Solve(orders, nil, 0)
	if len(got.Entries) != 1 || got.MaxLateness != 2 || got.Entries[0].Start != 0 || got.Entries[0].End != 5 {
		t.Fatalf("single: got %+v", got)
	}
}

// TestMaxOrdersSmoke runs the largest supported batch size and checks
// the timetable is internally consistent (cannot brute-force 12!).
func TestMaxOrdersSmoke(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	n := MaxOrders
	orders := make([]Order, n)
	for i := range orders {
		orders[i] = Order{ID: fmt.Sprintf("J%d", i), Type: rng.Intn(4), Duration: 1 + rng.Intn(9), Deadline: rng.Intn(60)}
	}
	setup := make([][]int, 4)
	for a := range setup {
		setup[a] = make([]int, 4)
		for b := range setup[a] {
			setup[a][b] = rng.Intn(10)
		}
	}
	got := Solve(orders, setup, 2)
	if len(got.Entries) != n {
		t.Fatalf("got %d entries, want %d", len(got.Entries), n)
	}
	seen := make(map[int]bool)
	for _, e := range got.Entries {
		if seen[e.Index] {
			t.Fatalf("order %d scheduled twice", e.Index)
		}
		seen[e.Index] = true
	}
	// Re-evaluating the returned permutation must reproduce the costs.
	re := Evaluate(orders, setup, 2, indices(got))
	if re.MaxLateness != got.MaxLateness || re.TotalSetup != got.TotalSetup {
		t.Fatalf("inconsistent: %+v vs %+v", got, re)
	}
}

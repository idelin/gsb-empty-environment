package sched

import (
	"math/rand"
	"reflect"
	"testing"
)

// randomProblem builds an instance with an asymmetric setup matrix.
func randomProblem(rng *rand.Rand, n, nTypes int) Problem {
	setup := make([][]int, nTypes)
	for i := range setup {
		setup[i] = make([]int, nTypes)
		for j := range setup[i] {
			if i != j {
				setup[i][j] = rng.Intn(10) // deliberately asymmetric
			}
		}
	}
	orders := make([]Order, n)
	total := 0
	for i := range orders {
		d := 1 + rng.Intn(9)
		total += d + 9
		orders[i] = Order{
			ID:       string(rune('A' + i)),
			Type:     rng.Intn(nTypes),
			Duration: d,
			Deadline: rng.Intn(total), // mix of tight and loose due dates
		}
	}
	return Problem{Orders: orders, Setup: setup, InitialType: rng.Intn(nTypes)}
}

func sameSolution(a, b Solution) bool {
	return a.MaxLateness == b.MaxLateness &&
		a.TotalSetup == b.TotalSetup &&
		reflect.DeepEqual(a.Sequence(), b.Sequence())
}

// TestSolveMatchesBruteForce compares the DP against full permutation
// enumeration on many small random instances with asymmetric setups.
func TestSolveMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 300; iter++ {
		n := 1 + rng.Intn(8) // 1..8 orders
		p := randomProblem(rng, n, 1+rng.Intn(3))
		fast, err := Solve(p)
		if err != nil {
			t.Fatalf("iter %d: Solve: %v", iter, err)
		}
		slow, err := SolveBruteForce(p)
		if err != nil {
			t.Fatalf("iter %d: SolveBruteForce: %v", iter, err)
		}
		if !sameSolution(fast, slow) {
			t.Fatalf("iter %d mismatch\nproblem: %+v\n DP:  lateness=%d setup=%d seq=%v\n BF:  lateness=%d setup=%d seq=%v",
				iter, p, fast.MaxLateness, fast.TotalSetup, fast.Sequence(),
				slow.MaxLateness, slow.TotalSetup, slow.Sequence())
		}
	}
}

// TestAsymmetricSetup checks that changeover direction matters: going
// 1->0 costs 9 while 0->1 costs 1, and the machine starts in type 1.
// Deadlines are tight enough that lateness ties, so the setup objective
// must pick B (type 1) before A (type 0).
func TestAsymmetricSetup(t *testing.T) {
	p := Problem{
		Orders: []Order{
			{ID: "A", Type: 0, Duration: 5, Deadline: 100},
			{ID: "B", Type: 1, Duration: 5, Deadline: 100},
		},
		Setup: [][]int{
			{0, 1},
			{9, 0},
		},
		InitialType: 1,
	}
	sol, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	// [B,A]: setups 0 + 9 = 9, finishes 5 and 19 -> max lateness -81.
	// [A,B]: setups 9 + 1 = 10, finishes 14 and 20 -> max lateness -80.
	// Lmax alone already prefers [B,A]; the point is the solver must not
	// assume Setup[i][j] == Setup[j][i].
	if got := sol.Sequence(); !reflect.DeepEqual(got, []int{1, 0}) {
		t.Fatalf("sequence = %v, want [1 0]", got)
	}
	if sol.TotalSetup != 9 {
		t.Fatalf("total setup = %d, want 9", sol.TotalSetup)
	}
	if sol.MaxLateness != -81 {
		t.Fatalf("max lateness = %d, want -81", sol.MaxLateness)
	}
}

// TestSetupBreaksLatenessTie builds a case where both orders of the tail
// give the same max lateness (0, caused by the urgent first order), so the
// asymmetric setup matrix decides: 2->1 costs 1, 1->2 costs 5.
func TestSetupBreaksLatenessTie(t *testing.T) {
	p := Problem{
		Orders: []Order{
			{ID: "urgent", Type: 0, Duration: 1, Deadline: 1},
			{ID: "t1", Type: 1, Duration: 1, Deadline: 1000},
			{ID: "t2", Type: 2, Duration: 1, Deadline: 1000},
		},
		Setup: [][]int{
			{0, 0, 0},
			{0, 0, 5},
			{0, 1, 0},
		},
		InitialType: 0,
	}
	sol, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	// urgent must go first (anything else makes it late). Then t2 before t1
	// costs 0+1=1 of setup vs 0+5=5 the other way; both keep max lateness 0.
	want := []int{0, 2, 1}
	if got := sol.Sequence(); !reflect.DeepEqual(got, want) {
		t.Fatalf("sequence = %v, want %v", got, want)
	}
	if sol.MaxLateness != 0 || sol.TotalSetup != 1 {
		t.Fatalf("lateness=%d setup=%d, want 0 and 1", sol.MaxLateness, sol.TotalSetup)
	}
}

// TestTieBreakOrder: fully symmetric instance (zero setups, identical
// orders) — every permutation is equally good, so the solver must return
// the lexicographically smallest index sequence.
func TestTieBreakOrder(t *testing.T) {
	orders := make([]Order, 4)
	for i := range orders {
		orders[i] = Order{Type: 0, Duration: 3, Deadline: 10}
	}
	p := Problem{
		Orders:      orders,
		Setup:       [][]int{{0}},
		InitialType: 0,
	}
	sol, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	want := []int{0, 1, 2, 3}
	if got := sol.Sequence(); !reflect.DeepEqual(got, want) {
		t.Fatalf("sequence = %v, want %v", got, want)
	}
}

// TestTimetableConsistency verifies the reported timetable arithmetic.
func TestTimetableConsistency(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for iter := 0; iter < 50; iter++ {
		p := randomProblem(rng, 1+rng.Intn(8), 1+rng.Intn(3))
		sol, err := Solve(p)
		if err != nil {
			t.Fatal(err)
		}
		time, prev, setupSum, maxLate := 0, p.InitialType, 0, 0
		for i, sl := range sol.Slots {
			if sl.Setup != p.Setup[prev][sl.Order.Type] {
				t.Fatalf("iter %d slot %d: setup mismatch", iter, i)
			}
			if sl.Start != time+sl.Setup || sl.Finish != sl.Start+sl.Order.Duration {
				t.Fatalf("iter %d slot %d: start/finish mismatch", iter, i)
			}
			if sl.Lateness != sl.Finish-sl.Order.Deadline {
				t.Fatalf("iter %d slot %d: lateness mismatch", iter, i)
			}
			setupSum += sl.Setup
			if i == 0 || sl.Lateness > maxLate {
				maxLate = sl.Lateness
			}
			time, prev = sl.Finish, sl.Order.Type
		}
		if setupSum != sol.TotalSetup || maxLate != sol.MaxLateness {
			t.Fatalf("iter %d: totals mismatch", iter)
		}
	}
}

func TestValidation(t *testing.T) {
	if _, err := Solve(Problem{Orders: make([]Order, MaxOrders+1), Setup: [][]int{{0}}}); err == nil {
		t.Fatal("expected error for too many orders")
	}
	if _, err := Solve(Problem{Orders: []Order{{Type: 0}}, Setup: [][]int{{0}}, InitialType: 3}); err == nil {
		t.Fatal("expected error for bad initial type")
	}
	if _, err := Solve(Problem{Orders: []Order{{Type: 1}}, Setup: [][]int{{0}}}); err == nil {
		t.Fatal("expected error for bad order type")
	}
	if _, err := Solve(Problem{Orders: []Order{{Type: 0}}, Setup: [][]int{{0, 1}}}); err == nil {
		t.Fatal("expected error for ragged setup matrix")
	}
	if _, err := Solve(Problem{Orders: []Order{{Type: 0, Duration: -1}}, Setup: [][]int{{0}}}); err == nil {
		t.Fatal("expected error for negative duration")
	}
	sol, err := Solve(Problem{})
	if err != nil || len(sol.Slots) != 0 {
		t.Fatalf("empty problem: sol=%v err=%v", sol, err)
	}
}

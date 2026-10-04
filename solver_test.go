package main

import (
	"fmt"
	"math/rand"
	"testing"
)

// bruteForce enumerates every slot assignment and returns the true
// lexicographic optimum (maxPenalty, totalPenalty). Only for small tests.
func bruteForce(in *Input, slots int) (int, int, bool) {
	n := len(in.Exams)
	table := penaltyTable(in, n)
	studentExams := map[string][]int{}
	for i, ex := range in.Exams {
		for _, st := range ex.Students {
			studentExams[st] = append(studentExams[st], i)
		}
	}
	sizes := make([]int, n)
	for i, ex := range in.Exams {
		sizes[i] = len(ex.Students)
	}
	assign := make([]int, n)
	bestMax, bestTotal := 1<<30, 1<<30
	found := false
	var rec func(k int)
	rec = func(k int) {
		if k == n {
			// room feasibility per slot
			for s := 0; s < slots; s++ {
				var sz []int
				for e := 0; e < n; e++ {
					if assign[e] == s {
						sz = append(sz, sizes[e])
					}
				}
				if len(sz) > 0 && assignRooms(sz, in.Rooms) == nil {
					return
				}
			}
			maxPen, total := 0, 0
			for _, exams := range studentExams {
				seen := map[int]bool{}
				slotsOf := make([]int, len(exams))
				for i, e := range exams {
					if seen[assign[e]] {
						return // same-slot conflict
					}
					seen[assign[e]] = true
					slotsOf[i] = assign[e]
				}
				p := studentPenalty(slotsOf, in.SlotsPerDay, table)
				if p > maxPen {
					maxPen = p
				}
				total += p
			}
			found = true
			if lexLess(maxPen, total, bestMax, bestTotal) {
				bestMax, bestTotal = maxPen, total
			}
			return
		}
		for s := 0; s < slots; s++ {
			assign[k] = s
			rec(k + 1)
		}
	}
	rec(0)
	return bestMax, bestTotal, found
}

func TestSimpleRunOfThree(t *testing.T) {
	in := &Input{
		Slots: 3,
		Rooms: []Room{{"R1", 10}},
		Exams: []Exam{
			{ID: "A", Students: []string{"x"}},
			{ID: "B", Students: []string{"x"}},
			{ID: "C", Students: []string{"x"}},
		},
	}
	sol, err := Solve(in, 0)
	if err != nil {
		t.Fatal(err)
	}
	// x's three exams must occupy slots 0,1,2 -> one run of 3 -> penalty 3.
	if sol.MaxPenalty != 3 || sol.TotalPenalty != 3 {
		t.Fatalf("got max=%d total=%d, want 3/3", sol.MaxPenalty, sol.TotalPenalty)
	}
	if probs := verify(in, sol); len(probs) != 0 {
		t.Fatalf("verify failed: %v", probs)
	}
}

func TestDayBoundaryBreaksRun(t *testing.T) {
	in := &Input{
		Slots:       4,
		SlotsPerDay: 2,
		Rooms:       []Room{{"R1", 10}},
		Exams: []Exam{
			{ID: "A", Students: []string{"x"}},
			{ID: "B", Students: []string{"x"}},
			{ID: "C", Students: []string{"x"}},
		},
	}
	sol, err := Solve(in, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Slots 1 and 2 are on different days, so x can have exams in slots
	// 0,1 (day 0) and 2 (day 1): only one adjacent pair -> penalty 1.
	if sol.MaxPenalty != 1 {
		t.Fatalf("got max=%d, want 1", sol.MaxPenalty)
	}
	if probs := verify(in, sol); len(probs) != 0 {
		t.Fatalf("verify failed: %v", probs)
	}
}

func TestMinimaxBeatsTotalOnly(t *testing.T) {
	// With 3 slots and these enrolments, concentrating all consecutive
	// exams on one student would give a lower-or-equal total but a higher
	// max. The solver must cap the individual penalty at 1.
	in := &Input{
		Slots: 3,
		Rooms: []Room{{"R1", 10}, {"R2", 10}},
		Exams: []Exam{
			{ID: "A", Students: []string{"x", "z"}},
			{ID: "B", Students: []string{"x"}},
			{ID: "C", Students: []string{"y", "z"}},
			{ID: "D", Students: []string{"y"}},
		},
	}
	sol, err := Solve(in, 0)
	if err != nil {
		t.Fatal(err)
	}
	if sol.MaxPenalty > 1 {
		t.Fatalf("max penalty %d exceeds 1: a few students bear all consecutive exams", sol.MaxPenalty)
	}
	bfMax, bfTotal, ok := bruteForce(in, 3)
	if !ok {
		t.Fatal("brute force found no solution")
	}
	if sol.MaxPenalty != bfMax || sol.TotalPenalty != bfTotal {
		t.Fatalf("solver (%d,%d) != brute force (%d,%d)", sol.MaxPenalty, sol.TotalPenalty, bfMax, bfTotal)
	}
}

func TestCrossCheckRandomInstances(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 30; trial++ {
		n := 4 + rng.Intn(4) // 4..7 exams
		slots := 2 + rng.Intn(3)
		var exams []Exam
		for i := 0; i < n; i++ {
			var sts []string
			for j := 0; j < 4; j++ {
				st := fmt.Sprintf("s%d", rng.Intn(6))
				dup := false
				for _, x := range sts {
					if x == st {
						dup = true
					}
				}
				if !dup {
					sts = append(sts, st)
				}
			}
			if len(sts) == 0 {
				sts = []string{"s0"}
			}
			exams = append(exams, Exam{ID: fmt.Sprintf("E%d", i), Students: sts})
		}
		in := &Input{
			Slots:       slots,
			SlotsPerDay: []int{0, 2}[rng.Intn(2)],
			Rooms:       []Room{{"R1", 10}, {"R2", 6}},
			Exams:       exams,
		}
		sol, err := solveFixed(in, slots, 0)
		if err != nil {
			t.Fatal(err)
		}
		bfMax, bfTotal, found := bruteForce(in, slots)
		if (sol != nil) != found {
			t.Fatalf("trial %d: feasibility mismatch (solver=%v brute=%v)\n%+v", trial, sol != nil, found, in)
		}
		if sol == nil {
			continue
		}
		if sol.MaxPenalty != bfMax || sol.TotalPenalty != bfTotal {
			t.Fatalf("trial %d: solver (%d,%d) != brute force (%d,%d)\n%+v", trial, sol.MaxPenalty, sol.TotalPenalty, bfMax, bfTotal, in)
		}
		if probs := verify(in, sol); len(probs) != 0 {
			t.Fatalf("trial %d: verify failed: %v", trial, probs)
		}
	}
}

func TestVerifyDetectsConflict(t *testing.T) {
	in := &Input{
		Slots: 2,
		Rooms: []Room{{"R1", 10}},
		Exams: []Exam{
			{ID: "A", Students: []string{"x"}},
			{ID: "B", Students: []string{"x"}},
		},
	}
	bad := &Solution{
		Slots: 2,
		Placements: []Placement{
			{Exam: "A", Slot: 0, Room: "R1", Enrollment: 1},
			{Exam: "B", Slot: 0, Room: "R1", Enrollment: 1},
		},
	}
	probs := verify(in, bad)
	if len(probs) == 0 {
		t.Fatal("verify should detect same-slot conflict and room double-booking")
	}
}

func TestInfeasibleRoomCapacity(t *testing.T) {
	in := &Input{
		Slots: 2,
		Rooms: []Room{{"R1", 5}},
		Exams: []Exam{
			{ID: "A", Students: []string{"s1", "s2", "s3", "s4", "s5", "s6"}},
		},
	}
	if _, err := Solve(in, 0); err == nil {
		t.Fatal("expected infeasible error for undersized room")
	}
}

func TestTooManyExams(t *testing.T) {
	in := &Input{Slots: 1, Rooms: []Room{{"R1", 100}}}
	for i := 0; i < maxExams+1; i++ {
		in.Exams = append(in.Exams, Exam{ID: fmt.Sprintf("E%d", i), Students: []string{fmt.Sprintf("s%d", i)}})
	}
	if _, err := Solve(in, 0); err == nil {
		t.Fatal("expected error for more than 12 exams")
	}
}

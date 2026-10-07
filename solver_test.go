package main

import (
	"math/rand"
	"testing"
)

func startsEq(got, want []int) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// Zero-duration jobs occupy no slot but precedence still binds.
func TestZeroDurationPrecedence(t *testing.T) {
	inst := &Instance{
		Horizon:   10,
		Resources: []Resource{{Name: "R", Capacity: 1}},
		Jobs: []Job{
			{Duration: 2, Demands: []int{1}},                         // 0
			{Duration: 0, Demands: []int{0}, Predecessors: []int{0}}, // 1: milestone
			{Duration: 1, Demands: []int{1}, Predecessors: []int{1}}, // 2
		},
	}
	res := Solve(inst)
	if res.Status != "optimal" {
		t.Fatalf("status=%s reason=%s", res.Status, res.Reason)
	}
	if res.Makespan != 3 {
		t.Fatalf("makespan=%d, want 3", res.Makespan)
	}
	if !startsEq(res.Starts, []int{0, 2, 2}) {
		t.Fatalf("starts=%v, want [0 2 2]", res.Starts)
	}
}

// A zero-duration chain of milestones must all sit at the same time.
func TestZeroDurationChain(t *testing.T) {
	inst := &Instance{
		Horizon:   5,
		Resources: []Resource{{Name: "R", Capacity: 1}},
		Jobs: []Job{
			{Duration: 1, Demands: []int{1}},
			{Duration: 0, Demands: []int{0}, Predecessors: []int{0}},
			{Duration: 0, Demands: []int{0}, Predecessors: []int{1}},
			{Duration: 1, Demands: []int{1}, Predecessors: []int{2}},
		},
	}
	res := Solve(inst)
	if res.Status != "optimal" || !startsEq(res.Starts, []int{0, 1, 1, 1}) {
		t.Fatalf("got %+v", res)
	}
}

func TestInfeasibleDemandExceedsCapacity(t *testing.T) {
	inst := &Instance{
		Horizon:   10,
		Resources: []Resource{{Name: "R", Capacity: 2}},
		Jobs:      []Job{{Duration: 3, Demands: []int{3}}},
	}
	if res := Solve(inst); res.Status != "infeasible" {
		t.Fatalf("status=%s, want infeasible", res.Status)
	}
}

func TestInfeasibleCycle(t *testing.T) {
	inst := &Instance{
		Horizon:   10,
		Resources: []Resource{{Name: "R", Capacity: 1}},
		Jobs: []Job{
			{Duration: 1, Demands: []int{1}, Predecessors: []int{1}},
			{Duration: 1, Demands: []int{1}, Predecessors: []int{0}},
		},
	}
	if res := Solve(inst); res.Status != "infeasible" {
		t.Fatalf("status=%s, want infeasible", res.Status)
	}
}

func TestInfeasibleHorizonTooTight(t *testing.T) {
	inst := &Instance{
		Horizon:   2,
		Resources: []Resource{{Name: "R", Capacity: 1}},
		Jobs: []Job{
			{Duration: 2, Demands: []int{1}},
			{Duration: 2, Demands: []int{1}},
		},
	}
	if res := Solve(inst); res.Status != "infeasible" {
		t.Fatalf("status=%s, want infeasible", res.Status)
	}
}

// Two resources, two jobs: each job needs both resources and each
// resource only fits one job at a time, forcing serialization even
// though no single resource alone explains the full makespan.
func TestMultiResourceConflict(t *testing.T) {
	inst := &Instance{
		Horizon: 10,
		Resources: []Resource{
			{Name: "A", Capacity: 2},
			{Name: "B", Capacity: 2},
		},
		Jobs: []Job{
			{Duration: 2, Demands: []int{2, 1}},
			{Duration: 2, Demands: []int{1, 2}},
			{Duration: 1, Demands: []int{1, 1}},
		},
	}
	res := Solve(inst)
	if res.Status != "optimal" {
		t.Fatalf("status=%s reason=%s", res.Status, res.Reason)
	}
	// Jobs 0 and 1 cannot overlap (A: 2+1>2, B: 1+2>2); job 2 fits
	// alongside neither (A: 2+1>2 with job0, B: 2+1>2 with job1).
	if res.Makespan != 5 {
		t.Fatalf("makespan=%d, want 5", res.Makespan)
	}
	if !startsEq(res.Starts, []int{0, 2, 4}) {
		t.Fatalf("starts=%v, want [0 2 4]", res.Starts)
	}
	// Evidence must never exceed capacity.
	for _, ev := range res.Timeline {
		for r, u := range ev.Usage {
			if u > inst.Resources[r].Capacity {
				t.Fatalf("t=%d resource %d usage %d exceeds capacity", ev.Time, r, u)
			}
		}
	}
}

// Lexicographic tie-break: two identical unconstrained jobs and room
// for only one at a time; both orders are optimal, [0 1] wins.
func TestLexTieBreak(t *testing.T) {
	inst := &Instance{
		Horizon:   6,
		Resources: []Resource{{Name: "R", Capacity: 1}},
		Jobs: []Job{
			{Duration: 1, Demands: []int{1}},
			{Duration: 1, Demands: []int{1}},
		},
	}
	res := Solve(inst)
	if res.Makespan != 2 || !startsEq(res.Starts, []int{0, 1}) {
		t.Fatalf("got makespan=%d starts=%v, want 2 [0 1]", res.Makespan, res.Starts)
	}
}

// Timeline evidence must reconstruct exactly the jobs' occupancy.
func TestTimelineEvidence(t *testing.T) {
	inst := &Instance{
		Horizon:   8,
		Resources: []Resource{{Name: "R", Capacity: 3}},
		Jobs: []Job{
			{Duration: 2, Demands: []int{2}},
			{Duration: 2, Demands: []int{1}},
		},
	}
	res := Solve(inst)
	if res.Makespan != 2 || !startsEq(res.Starts, []int{0, 0}) {
		t.Fatalf("got makespan=%d starts=%v", res.Makespan, res.Starts)
	}
	if len(res.Timeline) != 2 {
		t.Fatalf("timeline len=%d, want 2", len(res.Timeline))
	}
	for _, ev := range res.Timeline {
		if ev.Usage[0] != 3 || len(ev.Jobs) != 2 {
			t.Fatalf("t=%d usage=%v jobs=%v, want usage 3 and both jobs", ev.Time, ev.Usage, ev.Jobs)
		}
	}
}

// Randomized cross-check of Solve against exhaustive enumeration on
// small instances, covering infeasible cases and multi-resource
// conflicts.
func TestCrossCheckBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 400; trial++ {
		n := 1 + rng.Intn(5) // 1..5 jobs
		k := 1 + rng.Intn(2) // 1..2 resources
		inst := &Instance{Horizon: 1 + rng.Intn(7)}
		for r := 0; r < k; r++ {
			inst.Resources = append(inst.Resources, Resource{Capacity: rng.Intn(4)})
		}
		for i := 0; i < n; i++ {
			j := Job{Duration: rng.Intn(4), Demands: make([]int, k)}
			for r := 0; r < k; r++ {
				j.Demands[r] = rng.Intn(3)
			}
			// Random acyclic predecessors (only earlier indices).
			for p := 0; p < i; p++ {
				if rng.Intn(3) == 0 {
					j.Predecessors = append(j.Predecessors, p)
				}
			}
			inst.Jobs = append(inst.Jobs, j)
		}
		got := Solve(inst)
		want := BruteSolve(inst)
		if got.Status != want.Status {
			t.Fatalf("trial %d: status %s vs brute %s\ninst=%s", trial, got.Status, want.Status, mustJSON(inst))
		}
		if got.Status != "optimal" {
			continue
		}
		if got.Makespan != want.Makespan {
			t.Fatalf("trial %d: makespan %d vs brute %d\ninst=%s", trial, got.Makespan, want.Makespan, mustJSON(inst))
		}
		if !startsEq(got.Starts, want.Starts) {
			t.Fatalf("trial %d: starts %v vs brute %v\ninst=%s", trial, got.Starts, want.Starts, mustJSON(inst))
		}
	}
}

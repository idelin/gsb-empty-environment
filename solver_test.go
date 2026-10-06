package main

import (
	"math/rand"
	"testing"
)

func solveOrDie(t *testing.T, inst *Instance) *Result {
	t.Helper()
	res, err := Solve(inst)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	return res
}

func checkCost(t *testing.T, inst *Instance, res *Result) {
	t.Helper()
	// Recompute weighted tardiness from the schedule itself.
	last := map[int]int{}
	for slot, id := range res.Schedule {
		if id != Idle {
			last[id] = slot + 1
		}
	}
	var total int64
	for _, j := range inst.Jobs {
		c := j.Release
		if j.Processing > 0 {
			var ok bool
			c, ok = last[j.ID]
			if !ok {
				t.Fatalf("job %d with processing %d never scheduled", j.ID, j.Processing)
			}
		}
		if c > j.Deadline {
			total += int64(j.Weight) * int64(c-j.Deadline)
		}
	}
	if total != res.Cost {
		t.Fatalf("reported cost %d, recomputed %d", res.Cost, total)
	}
}

func TestIdleSlotsBeforeRelease(t *testing.T) {
	inst := &Instance{Slots: 5, Jobs: []Job{
		{ID: 1, Release: 3, Processing: 1, Deadline: 5, Weight: 7},
	}}
	res := solveOrDie(t, inst)
	want := []int{Idle, Idle, Idle, 1, Idle}
	for i := range want {
		if res.Schedule[i] != want[i] {
			t.Fatalf("schedule = %v, want %v", res.Schedule, want)
		}
	}
	if res.Cost != 0 {
		t.Fatalf("cost = %d, want 0", res.Cost)
	}
	checkCost(t, inst, res)
}

func TestZeroProcessingJob(t *testing.T) {
	// Job 2 needs no slots; it completes at its release and is tardy.
	inst := &Instance{Slots: 3, Jobs: []Job{
		{ID: 1, Release: 0, Processing: 2, Deadline: 2, Weight: 1},
		{ID: 2, Release: 2, Processing: 0, Deadline: 1, Weight: 5},
	}}
	res := solveOrDie(t, inst)
	if res.Cost != 5 {
		t.Fatalf("cost = %d, want 5 (zero-processing job tardy by 1)", res.Cost)
	}
	want := []int{1, 1, Idle}
	for i := range want {
		if res.Schedule[i] != want[i] {
			t.Fatalf("schedule = %v, want %v", res.Schedule, want)
		}
	}
	checkCost(t, inst, res)
}

func TestTieBreakLexicographic(t *testing.T) {
	// Zero weights: every feasible schedule costs 0; the per-slot id
	// sequence must be lexicographically smallest, jobs before idle.
	inst := &Instance{Slots: 4, Jobs: []Job{
		{ID: 2, Release: 0, Processing: 1, Deadline: 4, Weight: 0},
		{ID: 1, Release: 0, Processing: 1, Deadline: 4, Weight: 0},
	}}
	res := solveOrDie(t, inst)
	want := []int{1, 2, Idle, Idle}
	for i := range want {
		if res.Schedule[i] != want[i] {
			t.Fatalf("schedule = %v, want %v", res.Schedule, want)
		}
	}
}

func TestPreemptionOptimal(t *testing.T) {
	// Job 1 (released at 0) must be preempted by urgent heavy job 2.
	inst := &Instance{Slots: 4, Jobs: []Job{
		{ID: 1, Release: 0, Processing: 3, Deadline: 4, Weight: 1},
		{ID: 2, Release: 1, Processing: 1, Deadline: 2, Weight: 10},
	}}
	res := solveOrDie(t, inst)
	want := []int{1, 2, 1, 1}
	for i := range want {
		if res.Schedule[i] != want[i] {
			t.Fatalf("schedule = %v, want %v", res.Schedule, want)
		}
	}
	if res.Cost != 0 {
		t.Fatalf("cost = %d, want 0", res.Cost)
	}
	checkCost(t, inst, res)
}

func TestInfeasible(t *testing.T) {
	inst := &Instance{Slots: 2, Jobs: []Job{
		{ID: 1, Release: 0, Processing: 3, Deadline: 2, Weight: 1},
	}}
	if _, err := Solve(inst); err == nil {
		t.Fatal("expected infeasible error")
	}
}

func TestSlotsLimit(t *testing.T) {
	inst := &Instance{Slots: MaxSlots + 1}
	if _, err := Solve(inst); err == nil {
		t.Fatal("expected slots limit error")
	}
}

// TestCrossCheckRandom compares the memoized solver against exhaustive
// enumeration on random small instances that include zero-processing
// jobs and (via release dates and zero weights) idle slots.
func TestCrossCheckRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 400; trial++ {
		slots := 1 + rng.Intn(6)
		n := 1 + rng.Intn(3)
		jobs := make([]Job, n)
		total := 0
		for i := range jobs {
			r := rng.Intn(slots)
			p := rng.Intn(3) // includes zero processing
			if r+p > slots {
				p = slots - r
			}
			total += p
			jobs[i] = Job{
				ID:         i + 1,
				Release:    r,
				Processing: p,
				Deadline:   rng.Intn(slots + 3),
				Weight:     rng.Intn(6), // includes zero weight
			}
		}
		if total > slots {
			trial--
			continue
		}
		inst := &Instance{Slots: slots, Jobs: jobs}
		got, err := Solve(inst)
		if err != nil {
			if _, berr := bruteForce(inst); berr == nil {
				t.Fatalf("trial %d: Solve infeasible but brute force found a schedule (inst %+v)", trial, inst)
			}
			continue
		}
		want, err := bruteForce(inst)
		if err != nil {
			t.Fatalf("trial %d: bruteForce: %v (inst %+v)", trial, err, inst)
		}
		if got.Cost != want.Cost {
			t.Fatalf("trial %d: cost %d != brute force %d (inst %+v)", trial, got.Cost, want.Cost, inst)
		}
		if !sameSchedule(got.Schedule, want.Schedule) {
			t.Fatalf("trial %d: schedule %v != brute force %v (inst %+v)",
				trial, got.Schedule, want.Schedule, inst)
		}
		checkCost(t, inst, got)
	}
}

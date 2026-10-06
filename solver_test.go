package main

import (
	"math/rand"
	"reflect"
	"testing"
)

func TestBasicTardiness(t *testing.T) {
	// One task, p=2, deadline 1: completes at slot 1 right end => C=2, T=1.
	cost, sched := Solve([]Task{{ID: 1, Release: 0, Processing: 2, Deadline: 1, Weight: 3}}, 2)
	if cost != 3 {
		t.Fatalf("cost = %d, want 3", cost)
	}
	if !reflect.DeepEqual(sched, []int{1, 1}) {
		t.Fatalf("schedule = %v, want [1 1]", sched)
	}
}

func TestIdleSlotsBeforeRelease(t *testing.T) {
	// Task released at slot 2: slots 0 and 1 must be idle.
	cost, sched := Solve([]Task{{ID: 1, Release: 2, Processing: 1, Deadline: 5, Weight: 1}}, 3)
	if cost != 0 {
		t.Fatalf("cost = %d, want 0", cost)
	}
	if !reflect.DeepEqual(sched, []int{Idle, Idle, 1}) {
		t.Fatalf("schedule = %v, want [-1 -1 1]", sched)
	}
}

func TestZeroProcessing(t *testing.T) {
	// Zero-processing task completes at its release time (1), deadline 0:
	// constant cost 5 * (1 - 0) = 5, and it never occupies a slot.
	cost, sched := Solve([]Task{
		{ID: 1, Release: 1, Processing: 0, Deadline: 0, Weight: 5},
		{ID: 2, Release: 0, Processing: 1, Deadline: 10, Weight: 1},
	}, 1)
	if cost != 5 {
		t.Fatalf("cost = %d, want 5", cost)
	}
	if !reflect.DeepEqual(sched, []int{2}) {
		t.Fatalf("schedule = %v, want [2]", sched)
	}
}

func TestTieBreakLexicographic(t *testing.T) {
	// Identical tasks, no tardiness either way: [1 2] beats [2 1].
	cost, sched := Solve([]Task{
		{ID: 1, Release: 0, Processing: 1, Deadline: 100, Weight: 1},
		{ID: 2, Release: 0, Processing: 1, Deadline: 100, Weight: 1},
	}, 2)
	if cost != 0 {
		t.Fatalf("cost = %d, want 0", cost)
	}
	if !reflect.DeepEqual(sched, []int{1, 2}) {
		t.Fatalf("schedule = %v, want [1 2]", sched)
	}
}

func TestTieBreakIdleSortsFirst(t *testing.T) {
	// Idle (-1) orders before any task id, so idling first wins the tie.
	cost, sched := Solve([]Task{{ID: 5, Release: 0, Processing: 1, Deadline: 100, Weight: 1}}, 2)
	if cost != 0 {
		t.Fatalf("cost = %d, want 0", cost)
	}
	if !reflect.DeepEqual(sched, []int{Idle, 5}) {
		t.Fatalf("schedule = %v, want [-1 5]", sched)
	}
}

func TestPreemption(t *testing.T) {
	// Heavy task 2 released at slot 1 with tight deadline forces task 1
	// to be preempted: unique zero-cost schedule is [1 2 1 1].
	cost, sched := Solve([]Task{
		{ID: 1, Release: 0, Processing: 3, Deadline: 100, Weight: 1},
		{ID: 2, Release: 1, Processing: 1, Deadline: 2, Weight: 10},
	}, 4)
	if cost != 0 {
		t.Fatalf("cost = %d, want 0", cost)
	}
	if !reflect.DeepEqual(sched, []int{1, 2, 1, 1}) {
		t.Fatalf("schedule = %v, want [1 2 1 1]", sched)
	}
}

func TestIncompletePenalty(t *testing.T) {
	// p=3 in a horizon of 1 can never complete.
	cost, _ := Solve([]Task{{ID: 1, Release: 0, Processing: 3, Deadline: 0, Weight: 2}}, 1)
	want := incompletePenalty + 2*1
	if cost != want {
		t.Fatalf("cost = %d, want %d", cost, want)
	}
}

func TestZeroHorizon(t *testing.T) {
	cost, sched := Solve([]Task{{ID: 1, Release: 0, Processing: 0, Deadline: 0, Weight: 4}}, 0)
	if cost != 0 || len(sched) != 0 {
		t.Fatalf("cost = %d, sched = %v, want 0 and []", cost, sched)
	}
}

// bruteForce enumerates every schedule without memoization and returns the
// best (cost, sequence) under the same rules as Solve. Used to cross-check
// the DP on small instances, including idle slots and zero processing.
func bruteForce(tasks []Task, horizon int) (int64, []int) {
	var constCost int64
	active := make([]Task, 0, len(tasks))
	for _, tk := range tasks {
		if tk.Processing <= 0 {
			constCost += int64(tk.Weight) * max64(0, int64(tk.Release-tk.Deadline))
			continue
		}
		active = append(active, tk)
	}
	rem := make([]int, len(active))
	for i, tk := range active {
		rem[i] = tk.Processing
	}
	best := dpResult{cost: int64(1)<<62 - 1}
	var rec func(t int, seq []int, cost int64)
	rec = func(t int, seq []int, cost int64) {
		if cost > best.cost {
			return
		}
		if t == horizon {
			for i, tk := range active {
				if rem[i] > 0 {
					cost += incompletePenalty + int64(tk.Weight)*max64(0, int64(horizon-tk.Deadline))
				}
			}
			best = better(best, dpResult{cost: cost, seq: append([]int(nil), seq...)})
			return
		}
		rec(t+1, append(seq, Idle), cost)
		for i, tk := range active {
			if rem[i] == 0 || tk.Release > t {
				continue
			}
			rem[i]--
			c := cost
			if rem[i] == 0 {
				c += int64(tk.Weight) * max64(0, int64(t+1-tk.Deadline))
			}
			rec(t+1, append(seq, tk.ID), c)
			rem[i]++
		}
	}
	rec(0, nil, 0)
	return constCost + best.cost, best.seq
}

func sameSeq(a, b []int) bool {
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

func TestCrossCheckBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 300; trial++ {
		horizon := rng.Intn(5) // 0..4
		n := 1 + rng.Intn(3)   // 1..3 tasks
		tasks := make([]Task, n)
		for i := range tasks {
			tasks[i] = Task{
				ID:         i + 1,
				Release:    rng.Intn(horizon + 1),
				Processing: rng.Intn(4), // includes zero-processing cases
				Deadline:   rng.Intn(horizon + 2),
				Weight:     1 + rng.Intn(5),
			}
		}
		gotCost, gotSched := Solve(tasks, horizon)
		wantCost, wantSched := bruteForce(tasks, horizon)
		if gotCost != wantCost {
			t.Fatalf("trial %d: cost = %d, brute force = %d (tasks=%+v horizon=%d)",
				trial, gotCost, wantCost, tasks, horizon)
		}
		if !sameSeq(gotSched, wantSched) {
			t.Fatalf("trial %d: schedule = %v, brute force = %v (tasks=%+v horizon=%d)",
				trial, gotSched, wantSched, tasks, horizon)
		}
	}
}

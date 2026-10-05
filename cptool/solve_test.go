package main

import (
	"math"
	"strings"
	"testing"
)

func almostEq(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestSimpleChainRange(t *testing.T) {
	in := &Input{
		Origin: Interval{0, 0},
		Tasks: []Task{
			{ID: "A", Ready: Interval{0, 1}, Start: Interval{1, 3}, End: Interval{5, 8}},
			{ID: "B", Ready: Interval{5, 8}, Start: Interval{6, 9}, End: Interval{10, 14}, Deps: []string{"A"}},
		},
	}
	rep := Analyze(in)
	if !rep.Feasible {
		t.Fatalf("expected feasible, got %+v", rep.Conflict)
	}
	if !almostEq(rep.Min.Makespan, 10) {
		t.Errorf("min makespan = %v, want 10", rep.Min.Makespan)
	}
	if !almostEq(rep.Max.Makespan, 14) {
		t.Errorf("max makespan = %v, want 14", rep.Max.Makespan)
	}
	if !almostEq(rep.Max.Compute, 10) || !almostEq(rep.Max.Queue, 4) {
		t.Errorf("max attribution = compute %v / queue %v, want 10/4", rep.Max.Compute, rep.Max.Queue)
	}
	if !strings.Contains(rep.Max.Verdict, "compute-bound") {
		t.Errorf("max verdict = %q, want compute-bound", rep.Max.Verdict)
	}
}

func TestQueueBound(t *testing.T) {
	in := &Input{
		Origin: Interval{0, 0},
		Tasks: []Task{
			{ID: "A", Ready: Interval{0, 0}, Start: Interval{9, 10}, End: Interval{10, 11}},
		},
	}
	rep := Analyze(in)
	if !rep.Feasible {
		t.Fatalf("expected feasible, got %+v", rep.Conflict)
	}
	if !almostEq(rep.Min.Makespan, 10) || !almostEq(rep.Max.Makespan, 11) {
		t.Errorf("range = [%v, %v], want [10, 11]", rep.Min.Makespan, rep.Max.Makespan)
	}
	if !strings.Contains(rep.Max.Verdict, "queue-bound") {
		t.Errorf("max verdict = %q, want queue-bound", rep.Max.Verdict)
	}
	if rep.Max.Queue <= rep.Max.Compute {
		t.Errorf("queue %v should exceed compute %v", rep.Max.Queue, rep.Max.Compute)
	}
}

func TestConflictNegativeDurationLocated(t *testing.T) {
	// start cannot precede end: the tool must locate the contradiction
	// instead of reporting a negative run duration.
	in := &Input{
		Origin: Interval{0, 0},
		Tasks: []Task{
			{ID: "A", Ready: Interval{0, 1}, Start: Interval{9, 10}, End: Interval{3, 5}},
		},
	}
	rep := Analyze(in)
	if rep.Feasible {
		t.Fatal("expected infeasible")
	}
	if rep.Conflict.Kind != "bound" {
		t.Errorf("conflict kind = %q, want bound", rep.Conflict.Kind)
	}
	if !strings.Contains(rep.Conflict.Summary, "A.end") {
		t.Errorf("summary should name A.end, got %q", rep.Conflict.Summary)
	}
	joined := strings.Join(rep.Conflict.Chain, "\n")
	if !strings.Contains(joined, "A.start") {
		t.Errorf("chain should traverse A.start, got:\n%s", joined)
	}
}

func TestConflictThroughDependency(t *testing.T) {
	in := &Input{
		Origin: Interval{0, 0},
		Tasks: []Task{
			{ID: "A", Ready: Interval{0, 0}, Start: Interval{0, 0}, End: Interval{8, 9}},
			{ID: "B", Ready: Interval{0, 1}, Start: Interval{0, 1}, End: Interval{0, 1}, Deps: []string{"A"}},
		},
	}
	rep := Analyze(in)
	if rep.Feasible {
		t.Fatal("expected infeasible")
	}
	if !strings.Contains(rep.Conflict.Summary, "B.ready") {
		t.Errorf("summary should name B.ready, got %q", rep.Conflict.Summary)
	}
	joined := strings.Join(rep.Conflict.Chain, "\n")
	if !strings.Contains(joined, "A.end") {
		t.Errorf("chain should traverse A.end, got:\n%s", joined)
	}
}

func TestUncertainOrigin(t *testing.T) {
	in := &Input{
		Origin: Interval{0, 4},
		Tasks: []Task{
			{ID: "A", Ready: Interval{0, 10}, Start: Interval{0, 10}, End: Interval{6, 10}},
		},
	}
	rep := Analyze(in)
	if !rep.Feasible {
		t.Fatalf("expected feasible, got %+v", rep.Conflict)
	}
	// Shortest: origin at its latest feasible value 4, end at 6 -> 2.
	if !almostEq(rep.Min.Makespan, 2) {
		t.Errorf("min makespan = %v, want 2", rep.Min.Makespan)
	}
	// Longest: origin at 0, end at 10 -> 10.
	if !almostEq(rep.Max.Makespan, 10) {
		t.Errorf("max makespan = %v, want 10", rep.Max.Makespan)
	}
}

func TestParallelResourceContention(t *testing.T) {
	in := &Input{
		Origin: Interval{0, 0},
		Tasks: []Task{
			{ID: "build", Ready: Interval{0, 0}, Start: Interval{0, 2}, End: Interval{4, 6}},
			{ID: "test", Ready: Interval{4, 6}, Start: Interval{5, 9}, End: Interval{9, 15}, Deps: []string{"build"}},
			{ID: "docs", Ready: Interval{0, 0}, Start: Interval{0, 1}, End: Interval{1, 3}},
		},
	}
	rep := Analyze(in)
	if !rep.Feasible {
		t.Fatalf("expected feasible, got %+v", rep.Conflict)
	}
	if !almostEq(rep.Min.Makespan, 9) || !almostEq(rep.Max.Makespan, 15) {
		t.Errorf("range = [%v, %v], want [9, 15]", rep.Min.Makespan, rep.Max.Makespan)
	}
	// The critical chain must run through build -> test, not docs.
	for _, st := range rep.Max.Chain {
		if st.Task == "docs" {
			t.Errorf("docs should not be on the critical chain: %+v", rep.Max.Chain)
		}
	}
}

// checkWitness verifies a scenario timeline against every input constraint.
func checkWitness(t *testing.T, in *Input, sc *Scenario) {
	t.Helper()
	tm := map[string]float64{}
	for _, et := range sc.Timeline {
		tm[et.Event] = et.Time
	}
	if v := tm["origin"]; v < in.Origin.Lo-1e-6 || v > in.Origin.Hi+1e-6 {
		t.Errorf("origin @ %v outside [%v, %v]", v, in.Origin.Lo, in.Origin.Hi)
	}
	for _, task := range in.Tasks {
		evs := []struct {
			name string
			iv   Interval
		}{
			{task.ID + ".ready", task.Ready},
			{task.ID + ".start", task.Start},
			{task.ID + ".end", task.End},
		}
		for _, ev := range evs {
			v, ok := tm[ev.name]
			if !ok {
				t.Errorf("missing event %s in timeline", ev.name)
				continue
			}
			if v < ev.iv.Lo-1e-6 || v > ev.iv.Hi+1e-6 {
				t.Errorf("%s @ %v outside [%v, %v]", ev.name, v, ev.iv.Lo, ev.iv.Hi)
			}
		}
		if tm[task.ID+".start"] < tm[task.ID+".ready"]-1e-6 {
			t.Errorf("%s: start before ready", task.ID)
		}
		if tm[task.ID+".end"] < tm[task.ID+".start"]-1e-6 {
			t.Errorf("%s: end before start (negative run)", task.ID)
		}
		if tm[task.ID+".ready"] < tm["origin"]-1e-6 {
			t.Errorf("%s: ready before origin", task.ID)
		}
		for _, d := range task.Deps {
			if tm[task.ID+".ready"] < tm[d+".end"]-1e-6 {
				t.Errorf("%s: ready before dependency %s completed", task.ID, d)
			}
		}
	}
	var maxEnd float64
	for _, task := range in.Tasks {
		if tm[task.ID+".end"] > maxEnd {
			maxEnd = tm[task.ID+".end"]
		}
	}
	if !almostEq(sc.Makespan, maxEnd-tm["origin"]) {
		t.Errorf("makespan %v != max(end)-origin %v", sc.Makespan, maxEnd-tm["origin"])
	}
}

func TestWitnessesAreRealizable(t *testing.T) {
	in := &Input{
		Origin: Interval{0, 1},
		Tasks: []Task{
			{ID: "fetch", Ready: Interval{0, 1}, Start: Interval{0.5, 2}, End: Interval{2, 4}},
			{ID: "compile", Ready: Interval{2, 4}, Start: Interval{3, 6}, End: Interval{9, 13}, Deps: []string{"fetch"}},
			{ID: "lint", Ready: Interval{2, 4}, Start: Interval{2.5, 5}, End: Interval{5, 8}, Deps: []string{"fetch"}},
			{ID: "test", Ready: Interval{9, 13}, Start: Interval{10, 15}, End: Interval{18, 26}, Deps: []string{"compile", "lint"}},
		},
	}
	rep := Analyze(in)
	if !rep.Feasible {
		t.Fatalf("expected feasible, got %+v", rep.Conflict)
	}
	checkWitness(t, in, rep.Min)
	checkWitness(t, in, rep.Max)
	if rep.Min.Makespan > rep.Max.Makespan+1e-6 {
		t.Errorf("min %v exceeds max %v", rep.Min.Makespan, rep.Max.Makespan)
	}
}

func TestInputValidation(t *testing.T) {
	rep := Analyze(&Input{Origin: Interval{0, 0}})
	if rep.Feasible || rep.Conflict.Kind != "input" {
		t.Errorf("empty input should be an input conflict, got %+v", rep)
	}
	rep = Analyze(&Input{
		Origin: Interval{0, 0},
		Tasks:  []Task{{ID: "A", Ready: Interval{0, 1}, Start: Interval{0, 1}, End: Interval{0, 1}, Deps: []string{"ghost"}}},
	})
	if rep.Feasible || !strings.Contains(rep.Conflict.Summary, "ghost") {
		t.Errorf("unknown dependency should be located, got %+v", rep.Conflict)
	}
	rep = Analyze(&Input{
		Origin: Interval{0, 0},
		Tasks:  []Task{{ID: "A", Ready: Interval{5, 1}, Start: Interval{0, 1}, End: Interval{0, 1}}},
	})
	if rep.Feasible || rep.Conflict.Kind != "input" {
		t.Errorf("empty interval should be an input conflict, got %+v", rep.Conflict)
	}
}

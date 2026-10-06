package main

import (
	"math/rand"
	"testing"
)

func mustSolve(t *testing.T, in *Instance) Result {
	t.Helper()
	res := Solve(in)
	if !res.Feasible {
		t.Fatalf("expected feasible, got: %s", res.Reason)
	}
	return res
}

func expectStarts(t *testing.T, res Result, makespan int, starts ...int) {
	t.Helper()
	if res.Makespan != makespan {
		t.Fatalf("makespan = %d, want %d", res.Makespan, makespan)
	}
	if !equalInts(res.Starts, starts) {
		t.Fatalf("starts = %v, want %v", res.Starts, starts)
	}
}

func TestZeroDurationPrecedence(t *testing.T) {
	in := &Instance{
		Horizon:   6,
		Resources: []Resource{{Name: "R", Capacity: 1}},
		Jobs: []Job{
			{ID: "A", Duration: 2, Demand: map[string]int{"R": 1}},
			{ID: "B", Duration: 0, Preds: []string{"A"}},
			{ID: "C", Duration: 1, Preds: []string{"B"}, Demand: map[string]int{"R": 1}},
		},
	}
	res := mustSolve(t, in)
	expectStarts(t, res, 3, 0, 2, 2)
}

func TestMultiResourceConflict(t *testing.T) {
	// A and B overlap on R1 only partially: each needs 2 of R1 (cap 3) and
	// 1 of R2 (cap 1), so R2 forces full serialization despite R1 headroom.
	in := &Instance{
		Horizon: 8,
		Resources: []Resource{
			{Name: "R1", Capacity: 3},
			{Name: "R2", Capacity: 1},
		},
		Jobs: []Job{
			{ID: "A", Duration: 2, Demand: map[string]int{"R1": 2, "R2": 1}},
			{ID: "B", Duration: 2, Demand: map[string]int{"R1": 2, "R2": 1}},
		},
	}
	res := mustSolve(t, in)
	expectStarts(t, res, 4, 0, 2)
}

func TestLexicographicTieBreak(t *testing.T) {
	// Many schedules have makespan 2; the lex-min start vector puts A at 0,
	// then B as early as the single machine allows.
	in := &Instance{
		Horizon:   5,
		Resources: []Resource{{Name: "M", Capacity: 1}},
		Jobs: []Job{
			{ID: "A", Duration: 1, Demand: map[string]int{"M": 1}},
			{ID: "B", Duration: 1, Demand: map[string]int{"M": 1}},
			{ID: "C", Duration: 1, Demand: map[string]int{"M": 1}},
		},
	}
	res := mustSolve(t, in)
	expectStarts(t, res, 3, 0, 1, 2)
}

func TestInfeasibleDemandOverflow(t *testing.T) {
	in := &Instance{
		Horizon:   4,
		Resources: []Resource{{Name: "R", Capacity: 1}},
		Jobs:      []Job{{ID: "A", Duration: 1, Demand: map[string]int{"R": 2}}},
	}
	if res := Solve(in); res.Feasible {
		t.Fatalf("expected infeasible, got makespan %d", res.Makespan)
	}
}

func TestInfeasibleCycle(t *testing.T) {
	in := &Instance{
		Horizon:   10,
		Resources: []Resource{{Name: "R", Capacity: 2}},
		Jobs: []Job{
			{ID: "A", Duration: 1, Preds: []string{"C"}},
			{ID: "B", Duration: 1, Preds: []string{"A"}},
			{ID: "C", Duration: 1, Preds: []string{"B"}},
		},
	}
	res := Solve(in)
	if res.Feasible {
		t.Fatalf("expected infeasible, got makespan %d", res.Makespan)
	}
	if got := res.Reason; got == "" || got[:11] != "infeasible:" {
		t.Fatalf("unexpected reason: %q", got)
	}
}

func TestInfeasibleHorizon(t *testing.T) {
	in := &Instance{
		Horizon:   3,
		Resources: []Resource{{Name: "R", Capacity: 1}},
		Jobs: []Job{
			{ID: "A", Duration: 2, Demand: map[string]int{"R": 1}},
			{ID: "B", Duration: 2, Preds: []string{"A"}, Demand: map[string]int{"R": 1}},
		},
	}
	if res := Solve(in); res.Feasible {
		t.Fatalf("expected infeasible, got makespan %d", res.Makespan)
	}
}

func TestEvidenceProfileConsistent(t *testing.T) {
	in := &Instance{
		Horizon: 9,
		Resources: []Resource{
			{Name: "R1", Capacity: 2},
			{Name: "R2", Capacity: 3},
		},
		Jobs: []Job{
			{ID: "A", Duration: 3, Demand: map[string]int{"R1": 1, "R2": 2}},
			{ID: "B", Duration: 2, Preds: []string{"A"}, Demand: map[string]int{"R1": 1}},
			{ID: "C", Duration: 4, Demand: map[string]int{"R2": 1}},
		},
	}
	res := mustSolve(t, in)
	m, _ := buildModel(in)
	checkSchedule(t, m, res)
}

// checkSchedule independently validates a returned schedule against the model.
func checkSchedule(t *testing.T, m *model, res Result) {
	t.Helper()
	for u := 0; u < m.n; u++ {
		if res.Starts[u] < 0 || res.Starts[u]+m.dur[u] > res.Makespan {
			t.Fatalf("job %s outside makespan window", m.ids[u])
		}
		for _, p := range m.pred[u] {
			if res.Starts[p]+m.dur[p] > res.Starts[u] {
				t.Fatalf("precedence %s -> %s violated", m.ids[p], m.ids[u])
			}
		}
	}
	for ri := 0; ri < m.r; ri++ {
		for tt := 0; tt < res.Makespan; tt++ {
			used := 0
			for u := 0; u < m.n; u++ {
				if res.Starts[u] <= tt && tt < res.Starts[u]+m.dur[u] {
					used += m.dem[u][ri]
				}
			}
			if used > m.cap[ri] {
				t.Fatalf("resource %s over capacity at t=%d", m.rnames[ri], tt)
			}
		}
	}
}

// TestCrossCheckRandom compares Solve with BruteSolve on many small random
// instances, covering infeasible cases and multi-resource conflicts.
func TestCrossCheckRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(20261007))
	for trial := 0; trial < 3000; trial++ {
		in := randomInstance(rng)
		got := Solve(in)
		want := BruteSolve(in)
		if got.Feasible != want.Feasible {
			t.Fatalf("trial %d: feasibility mismatch solver=%v brute=%v\ninstance=%+v",
				trial, got.Feasible, want.Feasible, in)
		}
		if !got.Feasible {
			continue
		}
		if got.Makespan != want.Makespan || !equalInts(got.Starts, want.Starts) {
			t.Fatalf("trial %d: solver=(%d %v) brute=(%d %v)\ninstance=%+v",
				trial, got.Makespan, got.Starts, want.Makespan, want.Starts, in)
		}
		m, _ := buildModel(in)
		checkSchedule(t, m, got)
	}
}

func randomInstance(rng *rand.Rand) *Instance {
	n := 1 + rng.Intn(6)
	nr := 1 + rng.Intn(3)
	in := &Instance{Horizon: 1 + rng.Intn(8)}
	for i := 0; i < nr; i++ {
		in.Resources = append(in.Resources, Resource{
			Name:     string(rune('A' + i)),
			Capacity: 1 + rng.Intn(3),
		})
	}
	for j := 0; j < n; j++ {
		job := Job{ID: string(rune('a' + j)), Duration: rng.Intn(4)}
		if rng.Intn(10) == 0 {
			job.Duration = 0
		}
		for p := 0; p < j; p++ {
			if rng.Intn(3) == 0 {
				job.Preds = append(job.Preds, string(rune('a'+p)))
			}
		}
		if rng.Intn(20) == 0 && j > 0 {
			job.Preds = append(job.Preds, string(rune('a'+rng.Intn(j+1))))
		}
		job.Demand = map[string]int{}
		if job.Duration == 0 {
			in.Jobs = append(in.Jobs, job)
			continue
		}
		for _, rs := range in.Resources {
			d := rng.Intn(rs.Capacity + 1)
			if rng.Intn(15) == 0 {
				d = rs.Capacity + 1
			}
			if d > 0 {
				job.Demand[rs.Name] = d
			}
		}
		in.Jobs = append(in.Jobs, job)
	}
	return in
}

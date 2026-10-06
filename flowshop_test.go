package flowshop

import (
	"math/rand"
	"reflect"
	"testing"
)

func ids(jobs []Job) []int {
	out := make([]int, len(jobs))
	for i, j := range jobs {
		out[i] = j.ID
	}
	return out
}

func jobsByID(seq []int, pool []Job) []Job {
	out := make([]Job, len(seq))
	for i, id := range seq {
		for _, j := range pool {
			if j.ID == id {
				out[i] = j
			}
		}
	}
	return out
}

// Hand-computed schedules without downtime.
func TestManualNoDowntime(t *testing.T) {
	jobs := []Job{{ID: 1, P1: 2, P2: 3}, {ID: 2, P1: 5, P2: 1}}
	s := computeSchedule(jobs, nil, nil, machineState{})
	if s.Makespan != 8 {
		t.Fatalf("makespan = %d, want 8", s.Makespan)
	}
	if len(s.Blocking) != 0 {
		t.Fatalf("unexpected blocking: %+v", s.Blocking)
	}

	// Sequence [2,1] blocks machine 1: job 1 finishes on M1 at 2 but
	// machine 2 is busy with job 2 until 6.
	pair := []Job{{ID: 1, P1: 1, P2: 1}, {ID: 2, P1: 1, P2: 5}}
	rev := []Job{pair[1], pair[0]}
	s = computeSchedule(rev, nil, nil, machineState{})
	want := []BlockedPeriod{{JobID: 1, Start: 2, End: 6}}
	if !reflect.DeepEqual(s.Blocking, want) {
		t.Fatalf("blocking = %+v, want %+v", s.Blocking, want)
	}
	if s.Makespan != 7 {
		t.Fatalf("makespan = %d, want 7", s.Makespan)
	}
	if err := Verify(rev, nil, nil); err != nil {
		t.Fatal(err)
	}
}

// Hand-computed schedule with downtime: processing pauses, the machine
// stays occupied, and the buffer stays zero.
func TestManualDowntime(t *testing.T) {
	jobs := []Job{{ID: 1, P1: 4, P2: 2}, {ID: 2, P1: 1, P2: 1}}
	down1 := []Interval{{Start: 2, End: 5}}
	s := computeSchedule(jobs, down1, nil, machineState{})
	// Job 1 occupies M1 during [0,7): works [0,2), paused [2,5), works [5,7).
	if got := s.Timeline[0].ProcEndM1; got != 7 {
		t.Fatalf("job1 ProcEndM1 = %d, want 7", got)
	}
	if s.Makespan != 10 {
		t.Fatalf("makespan = %d, want 10", s.Makespan)
	}
	wantDown := []DowntimeEvidence{{Machine: 1, JobID: 1, Start: 2, End: 5}}
	if !reflect.DeepEqual(s.Downtime, wantDown) {
		t.Fatalf("downtime evidence = %+v, want %+v", s.Downtime, wantDown)
	}
	// Job 2 finishes M1 at 8 but M2 is busy with job 1 until 9.
	wantBlock := []BlockedPeriod{{JobID: 2, Start: 8, End: 9}}
	if !reflect.DeepEqual(s.Blocking, wantBlock) {
		t.Fatalf("blocking = %+v, want %+v", s.Blocking, wantBlock)
	}
	if err := Verify(jobs, down1, nil); err != nil {
		t.Fatal(err)
	}
}

// A job loaded while its machine is down occupies it and waits.
func TestLoadDuringDowntime(t *testing.T) {
	jobs := []Job{{ID: 1, P1: 2, P2: 1}}
	down1 := []Interval{{Start: 0, End: 3}}
	s := computeSchedule(jobs, down1, nil, machineState{})
	if s.Timeline[0].EnterM1 != 0 || s.Timeline[0].ProcEndM1 != 5 {
		t.Fatalf("timeline = %+v", s.Timeline[0])
	}
	if s.Makespan != 6 {
		t.Fatalf("makespan = %d, want 6", s.Makespan)
	}
	if err := Verify(jobs, down1, nil); err != nil {
		t.Fatal(err)
	}
}

// The recursive model and the discrete-event simulation must agree on
// every permutation of random instances, with and without downtime.
func TestModelEqualsEventSimAllPermutations(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 30; trial++ {
		n := 2 + rng.Intn(5) // 2..6 jobs
		jobs := make([]Job, n)
		for i := range jobs {
			jobs[i] = Job{ID: i + 1, P1: rng.Intn(9), P2: rng.Intn(9)}
		}
		down1 := randomDowntime(rng)
		down2 := randomDowntime(rng)
		perm := sortByID(jobs)
		for {
			p := append([]Job(nil), perm...)
			if err := Verify(p, down1, down2); err != nil {
				t.Fatalf("trial %d seq %v: %v", trial, ids(p), err)
			}
			if !nextPermutation(perm) {
				break
			}
		}
	}
}

func randomDowntime(rng *rand.Rand) []Interval {
	var out []Interval
	for k := rng.Intn(3); k >= 0; k-- {
		start := rng.Intn(20)
		out = append(out, Interval{Start: start, End: start + 1 + rng.Intn(5)})
	}
	return out
}

// Solve must return the optimum found by an independent brute force that
// evaluates every permutation with the event simulator, breaking ties by
// the lexicographically smallest ID sequence.
func TestSolveMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 20; trial++ {
		n := 2 + rng.Intn(5)
		jobs := make([]Job, n)
		for i := range jobs {
			jobs[i] = Job{ID: i + 1, P1: rng.Intn(9), P2: rng.Intn(9)}
		}
		down1 := randomDowntime(rng)
		down2 := randomDowntime(rng)

		res, err := Solve(jobs, down1, down2)
		if err != nil {
			t.Fatal(err)
		}
		wantMake, wantSeq := bruteForce(jobs, nil, down1, down2)
		if res.Schedule.Makespan != wantMake {
			t.Fatalf("trial %d: makespan = %d, brute force = %d", trial, res.Schedule.Makespan, wantMake)
		}
		if !reflect.DeepEqual(res.Sequence, wantSeq) {
			t.Fatalf("trial %d: sequence = %v, want %v", trial, res.Sequence, wantSeq)
		}
		if err := Verify(jobsByID(res.Sequence, jobs), down1, down2); err != nil {
			t.Fatal(err)
		}
	}
}

// bruteForce finds the optimal suffix order by simulating every
// permutation with the event simulator. The prefix, if any, is kept
// fixed in front.
func bruteForce(suffix, prefix []Job, down1, down2 []Interval) (int, []int) {
	d1 := normalizeDowntime(down1)
	d2 := normalizeDowntime(down2)
	perm := sortByID(suffix)
	best := -1
	var bestSeq []int
	for {
		full := append(append([]Job(nil), prefix...), perm...)
		ms := simulateEvents(full, d1, d2).Makespan
		if best < 0 || ms < best {
			best = ms
			bestSeq = ids(full)
		}
		if !nextPermutation(perm) {
			break
		}
	}
	return best, bestSeq
}

// Ties are broken by the lexicographically smallest job-ID sequence.
func TestTieBreakByID(t *testing.T) {
	jobs := []Job{{ID: 1, P1: 1, P2: 1}, {ID: 2, P1: 1, P2: 5}}
	// Both orders need 7 time units; [1,2] must win.
	res, err := Solve(jobs, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Schedule.Makespan != 7 {
		t.Fatalf("makespan = %d, want 7", res.Schedule.Makespan)
	}
	if !reflect.DeepEqual(res.Sequence, []int{1, 2}) {
		t.Fatalf("sequence = %v, want [1 2]", res.Sequence)
	}
}

// Replan keeps the started prefix fixed and re-optimises only the
// remaining suffix under the known downtime table.
func TestReplan(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	for trial := 0; trial < 20; trial++ {
		n := 4 + rng.Intn(3) // 4..6 jobs
		jobs := make([]Job, n)
		for i := range jobs {
			jobs[i] = Job{ID: i + 1, P1: rng.Intn(9), P2: rng.Intn(9)}
		}
		rng.Shuffle(n, func(i, k int) { jobs[i], jobs[k] = jobs[k], jobs[i] })
		split := 1 + rng.Intn(n-1)
		prefix := append([]Job(nil), jobs[:split]...)
		remaining := append([]Job(nil), jobs[split:]...)
		down1 := randomDowntime(rng)
		down2 := randomDowntime(rng)

		res, err := Replan(prefix, remaining, down1, down2)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(res.Sequence[:split], ids(prefix)) {
			t.Fatalf("trial %d: prefix changed to %v", trial, res.Sequence)
		}
		wantMake, wantSeq := bruteForce(remaining, prefix, down1, down2)
		if res.Schedule.Makespan != wantMake {
			t.Fatalf("trial %d: replan makespan = %d, brute force = %d", trial, res.Schedule.Makespan, wantMake)
		}
		if !reflect.DeepEqual(res.Sequence, wantSeq) {
			t.Fatalf("trial %d: sequence = %v, want %v", trial, res.Sequence, wantSeq)
		}
		if err := Verify(jobsByID(res.Sequence, jobs), down1, down2); err != nil {
			t.Fatal(err)
		}
	}
}

// Replan must not do worse than keeping the original order.
func TestReplanNeverWorse(t *testing.T) {
	prefix := []Job{{ID: 3, P1: 6, P2: 1}}
	remaining := []Job{{ID: 1, P1: 2, P2: 8}, {ID: 2, P1: 5, P2: 4}, {ID: 4, P1: 3, P2: 3}}
	down2 := []Interval{{Start: 9, End: 12}}
	res, err := Replan(prefix, remaining, nil, down2)
	if err != nil {
		t.Fatal(err)
	}
	kept := simulateEvents(append(append([]Job(nil), prefix...), remaining...), nil, normalizeDowntime(down2))
	if res.Schedule.Makespan > kept.Makespan {
		t.Fatalf("replan %d worse than original %d", res.Schedule.Makespan, kept.Makespan)
	}
	if res.Sequence[0] != 3 {
		t.Fatalf("prefix not preserved: %v", res.Sequence)
	}
}

func TestValidation(t *testing.T) {
	many := make([]Job, MaxJobs+1)
	for i := range many {
		many[i] = Job{ID: i + 1, P1: 1, P2: 1}
	}
	if _, err := Solve(many, nil, nil); err == nil {
		t.Fatal("expected error for more than 10 jobs")
	}
	dup := []Job{{ID: 1, P1: 1, P2: 1}, {ID: 1, P1: 2, P2: 2}}
	if _, err := Solve(dup, nil, nil); err == nil {
		t.Fatal("expected error for duplicate IDs")
	}
	neg := []Job{{ID: 1, P1: -1, P2: 1}}
	if _, err := Solve(neg, nil, nil); err == nil {
		t.Fatal("expected error for negative processing time")
	}
}

// The exact solver must handle the maximum of ten jobs
// (3,628,800 permutations).
func TestSolveTenJobs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping exhaustive 10-job search in short mode")
	}
	rng := rand.New(rand.NewSource(1))
	jobs := make([]Job, 10)
	for i := range jobs {
		jobs[i] = Job{ID: i + 1, P1: 1 + rng.Intn(9), P2: 1 + rng.Intn(9)}
	}
	res, err := Solve(jobs, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sequence) != 10 {
		t.Fatalf("sequence length = %d", len(res.Sequence))
	}
	if err := Verify(jobsByID(res.Sequence, jobs), nil, nil); err != nil {
		t.Fatal(err)
	}
}

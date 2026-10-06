package bnb

import (
	"bytes"
	"container/heap"
	"math/rand"
	"reflect"
	"testing"
)

func testProblem(seed int64, n int) *Problem {
	rng := rand.New(rand.NewSource(seed))
	return RandomProblem(rng, n, 20, 50, 3, 2, 5)
}

func solveUnlimited(t *testing.T, p *Problem) Result {
	t.Helper()
	s, err := NewSolver(p)
	if err != nil {
		t.Fatalf("NewSolver: %v", err)
	}
	res, err := s.Solve(Options{Budget: -1})
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	return res
}

// solveWithCheckpoints runs the solver in budget chunks, round-tripping a
// full checkpoint through a byte buffer after every chunk.
func solveWithCheckpoints(t *testing.T, p *Problem, chunk int64) (Result, int) {
	t.Helper()
	s, err := NewSolver(p)
	if err != nil {
		t.Fatalf("NewSolver: %v", err)
	}
	resumes := 0
	for {
		res, err := s.Solve(Options{Budget: chunk})
		if err != nil {
			t.Fatalf("Solve: %v", err)
		}
		if res.Status == StatusOptimal {
			return res, resumes
		}
		var buf bytes.Buffer
		if err := s.SaveCheckpoint(&buf); err != nil {
			t.Fatalf("SaveCheckpoint: %v", err)
		}
		s, err = LoadCheckpoint(&buf)
		if err != nil {
			t.Fatalf("LoadCheckpoint: %v", err)
		}
		resumes++
	}
}

// solveWithCrashes simulates a crash after every budget chunk: mid-run
// checkpoints are taken by the hook, and after each "crash" the in-memory
// solver is discarded and the last checkpoint is restored.
func solveWithCrashes(t *testing.T, p *Problem, chunk, checkpointEvery int64) (Result, int) {
	t.Helper()
	s, err := NewSolver(p)
	if err != nil {
		t.Fatalf("NewSolver: %v", err)
	}
	var last bytes.Buffer
	if err := s.SaveCheckpoint(&last); err != nil {
		t.Fatalf("SaveCheckpoint: %v", err)
	}
	crashes := 0
	for {
		res, err := s.Solve(Options{
			Budget:          chunk,
			CheckpointEvery: checkpointEvery,
			Hook: func(s *Solver) error {
				last.Reset()
				return s.SaveCheckpoint(&last)
			},
		})
		if err != nil {
			t.Fatalf("Solve: %v", err)
		}
		if res.Status == StatusOptimal {
			return res, crashes
		}
		s, err = LoadCheckpoint(&last)
		if err != nil {
			t.Fatalf("LoadCheckpoint: %v", err)
		}
		crashes++
	}
}

func assertSameResult(t *testing.T, name string, got, want Result) {
	t.Helper()
	if got.Status != want.Status ||
		got.LowerBound != want.LowerBound ||
		got.UpperBound != want.UpperBound ||
		got.NodesExplored != want.NodesExplored ||
		!reflect.DeepEqual(got.Solution, want.Solution) {
		t.Fatalf("%s mismatch:\n got:  %s sol=%v\n want: %s sol=%v",
			name, got, got.Solution, want, want.Solution)
	}
}

// TestResumeMatchesUninterrupted is the core exactly-once guarantee: a run
// chopped into budget chunks with a full checkpoint round-trip between
// chunks must expand exactly the same nodes and find exactly the same
// result as the uninterrupted run.
func TestResumeMatchesUninterrupted(t *testing.T) {
	for _, seed := range []int64{1, 2, 3, 7, 42} {
		p := testProblem(seed, 9)
		want := solveUnlimited(t, p)
		if want.Status != StatusOptimal {
			t.Fatalf("seed %d: unlimited run not optimal: %s", seed, want)
		}
		for _, chunk := range []int64{1, 3, 17, 100} {
			got, _ := solveWithCheckpoints(t, p, chunk)
			assertSameResult(t, "checkpointed run", got, want)
		}
	}
}

// TestCrashResume checks that restoring the last mid-run checkpoint after
// a simulated crash still reproduces the uninterrupted run exactly.
func TestCrashResume(t *testing.T) {
	for _, seed := range []int64{5, 11, 23} {
		p := testProblem(seed, 9)
		want := solveUnlimited(t, p)
		const chunk = 7
		got, crashes := solveWithCrashes(t, p, chunk, 3)
		if want.NodesExplored > chunk && crashes == 0 {
			t.Fatalf("seed %d: expected at least one crash (%d nodes, chunk %d)",
				seed, want.NodesExplored, chunk)
		}
		assertSameResult(t, "crash-resumed run", got, want)
	}
}

// TestExhaustiveVerification compares the solver's optimality claim
// against independent brute-force enumeration.
func TestExhaustiveVerification(t *testing.T) {
	for seed := int64(0); seed < 30; seed++ {
		p := testProblem(seed, 8)
		res := solveUnlimited(t, p)
		if res.Status != StatusOptimal {
			t.Fatalf("seed %d: %s", seed, res.Status)
		}
		if res.LowerBound != res.UpperBound {
			t.Fatalf("seed %d: optimal but bounds differ: %s", seed, res)
		}
		best, _ := Exhaustive(p)
		if res.LowerBound != best {
			t.Fatalf("seed %d: solver claims %d, exhaustive says %d", seed, res.LowerBound, best)
		}
	}
}

// TestBudgetBounds checks that a budget-exhausted run returns bounds that
// genuinely bracket the optimum.
func TestBudgetBounds(t *testing.T) {
	for seed := int64(0); seed < 20; seed++ {
		p := testProblem(seed, 10)
		s, err := NewSolver(p)
		if err != nil {
			t.Fatalf("NewSolver: %v", err)
		}
		res, err := s.Solve(Options{Budget: 5})
		if err != nil {
			t.Fatalf("Solve: %v", err)
		}
		best, _ := Exhaustive(p)
		if res.Status == StatusBudgetExhausted {
			if res.LowerBound > best || res.UpperBound < best {
				t.Fatalf("seed %d: bounds [%d,%d] do not bracket optimum %d",
					seed, res.LowerBound, res.UpperBound, best)
			}
			if res.FrontierSize == 0 {
				t.Fatalf("seed %d: budget exhausted with empty frontier", seed)
			}
		} else if res.LowerBound != best {
			t.Fatalf("seed %d: finished early with wrong value %d, want %d",
				seed, res.LowerBound, best)
		}
	}
}

// TestMidRunCheckpointEquivalence saves a checkpoint mid-run, then
// verifies that continuing in memory and continuing from the restored
// checkpoint produce identical results and identical node counts.
func TestMidRunCheckpointEquivalence(t *testing.T) {
	p := testProblem(99, 10)
	a, err := NewSolver(p)
	if err != nil {
		t.Fatalf("NewSolver: %v", err)
	}
	if _, err := a.Solve(Options{Budget: 13}); err != nil {
		t.Fatalf("Solve: %v", err)
	}
	var buf bytes.Buffer
	if err := a.SaveCheckpoint(&buf); err != nil {
		t.Fatalf("SaveCheckpoint: %v", err)
	}
	b, err := LoadCheckpoint(&buf)
	if err != nil {
		t.Fatalf("LoadCheckpoint: %v", err)
	}
	if b.NodesExplored() != a.NodesExplored() || b.FrontierSize() != a.FrontierSize() {
		t.Fatalf("restored state mismatch: nodes %d/%d frontier %d/%d",
			b.NodesExplored(), a.NodesExplored(), b.FrontierSize(), a.FrontierSize())
	}
	ra, err := a.Solve(Options{Budget: -1})
	if err != nil {
		t.Fatalf("Solve a: %v", err)
	}
	rb, err := b.Solve(Options{Budget: -1})
	if err != nil {
		t.Fatalf("Solve b: %v", err)
	}
	assertSameResult(t, "mid-run restore", rb, ra)
}

// TestPriorityOrder checks the frontier ordering directly: upper bound
// descending, then path encoding ascending.
func TestPriorityOrder(t *testing.T) {
	nodes := []*Node{
		{path: []int64{1, 0}, bound: 10},
		{path: []int64{0, 2}, bound: 10},
		{path: []int64{0, 1}, bound: 10},
		{path: []int64{5}, bound: 11},
		{path: []int64{0}, bound: 9},
	}
	var h nodeHeap
	for _, n := range nodes {
		h = append(h, n)
	}
	heap.Init(&h)
	var got []string
	for h.Len() > 0 {
		got = append(got, PathKey(heap.Pop(&h).(*Node).path))
	}
	want := []string{"5", "0.1", "0.2", "1.0", "0"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pop order = %v, want %v", got, want)
	}
}

// TestEdgeCases exercises degenerate instances against brute force.
func TestEdgeCases(t *testing.T) {
	cases := []*Problem{
		{Values: nil, Weights: nil, Bounds: nil, Capacity: 10},
		{Values: []int64{5}, Weights: []int64{4}, Bounds: []int64{0}, Capacity: 10},
		{Values: []int64{5, 7}, Weights: []int64{4, 3}, Bounds: []int64{2, 2}, Capacity: 0},
		// zero-weight items with positive value must be taken fully
		{Values: []int64{9, 3}, Weights: []int64{0, 5}, Bounds: []int64{4, 1}, Capacity: 6},
		// negative values are never taken
		{Values: []int64{-2, 6}, Weights: []int64{1, 2}, Bounds: []int64{3, 2}, Capacity: 4},
	}
	for i, p := range cases {
		res := solveUnlimited(t, p)
		best, _ := Exhaustive(p)
		if res.Status != StatusOptimal || res.LowerBound != best {
			t.Fatalf("case %d: got %s, exhaustive optimum %d", i, res, best)
		}
	}
}

// TestCorruptCheckpoint ensures structural corruption is rejected.
func TestCorruptCheckpoint(t *testing.T) {
	p := testProblem(1, 6)
	s, err := NewSolver(p)
	if err != nil {
		t.Fatalf("NewSolver: %v", err)
	}
	if _, err := s.Solve(Options{Budget: 3}); err != nil {
		t.Fatalf("Solve: %v", err)
	}
	var buf bytes.Buffer
	if err := s.SaveCheckpoint(&buf); err != nil {
		t.Fatalf("SaveCheckpoint: %v", err)
	}
	data := buf.Bytes()
	if _, err := LoadCheckpoint(bytes.NewReader(data[:len(data)/2])); err == nil {
		t.Fatal("truncated checkpoint accepted")
	}
}

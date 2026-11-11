package netsec

import (
	"errors"
	"math/rand"
	"testing"
)

func TestSolveBasic(t *testing.T) {
	// Two independent events may share the later event's bucket: one bucket.
	sol, err := Solve([]int{1, 5}, []int{10, 10}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	verifySolution(t, &Problem{Times: []int{1, 5}, MaxDelay: []int{10, 10}}, sol)
	if sol.DistinctBuckets != 1 || sol.TotalDelay != 4 {
		t.Fatalf("got buckets=%d delay=%d, want 1 and 4", sol.DistinctBuckets, sol.TotalDelay)
	}
	if sol.Buckets[0] != 5 || sol.Buckets[1] != 5 {
		t.Fatalf("unexpected bucket times: %v", sol.Buckets)
	}

	// A causal chain 1 -> 0 where event 1 cannot wait until time 10 must use
	// two buckets while preserving the causal order.
	sol, err = Solve([]int{10, 0}, []int{1, 9}, [][2]int{{1, 0}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	verifySolution(t, &Problem{
		Times:    []int{10, 0},
		MaxDelay: []int{1, 10},
		Edges:    [][2]int{{1, 0}},
	}, sol)
	if sol.DistinctBuckets != 2 {
		t.Fatalf("got %d buckets, want 2", sol.DistinctBuckets)
	}
	if sol.Buckets[1] >= sol.Buckets[0] || sol.Buckets[0] > 11 {
		t.Fatalf("causal ordering violated: %v", sol.Buckets)
	}

	// A cycle with conflicting zero-delay events is infeasible and carries a
	// witness path.
	_, err = Solve([]int{0, 5}, []int{0, 0}, [][2]int{{0, 1}, {1, 0}})
	var infeasible *InfeasibleError
	if err == nil {
		t.Fatal("expected an infeasibility error")
	}
	if !errors.As(err, &infeasible) {
		t.Fatalf("got error type %T, want *InfeasibleError", err)
	}
	if infeasible.LowerBound != 5 || infeasible.Limit != 0 {
		t.Fatalf("unexpected bounds: %+v", infeasible)
	}
	verifyWitness(t, &Problem{
		Times:    []int{0, 5},
		MaxDelay: []int{0, 0},
		Edges:    [][2]int{{0, 1}, {1, 0}},
	}, infeasible)
}

func TestSingleEventAndValidation(t *testing.T) {
	sol, err := Solve([]int{7}, []int{3}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sol.DistinctBuckets != 1 || sol.Buckets[0] != 7 || sol.TotalDelay != 0 {
		t.Fatalf("unexpected solution: %+v", sol)
	}

	if _, err := Solve(nil, nil, nil); err == nil {
		t.Fatal("empty input must be rejected")
	}
	if _, err := Solve([]int{1, 2}, []int{1}, nil); err == nil {
		t.Fatal("mismatched slice lengths must be rejected")
	}
	if _, err := Solve([]int{1}, []int{-1}, nil); err == nil {
		t.Fatal("negative delay must be rejected")
	}
	if _, err := Solve([]int{1, 2}, []int{1, 1}, [][2]int{{0, 2}}); err == nil {
		t.Fatal("out-of-range edge must be rejected")
	}
}

func TestExhaustiveOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(20261112))
	for trial := 0; trial < 800; trial++ {
		n := 1 + rng.Intn(4)
		prob := randomProblem(rng, n, 6, 4, 0.35)
		sol, err := prob.Solve()
		oracleBuckets, oracleDelay, oracleFeasible := bruteForce(prob)
		if !oracleFeasible {
			if err == nil {
				t.Fatalf("trial %d: solver returned %+v but no legal assignment exists; problem=%+v",
					trial, sol, prob)
			}
			var infeasible *InfeasibleError
			if !errors.As(err, &infeasible) {
				t.Fatalf("trial %d: error has wrong type: %v", trial, err)
			}
			verifyWitness(t, prob, infeasible)
			continue
		}
		if err != nil {
			t.Fatalf("trial %d: solver failed on a feasible problem: %v; problem=%+v", trial, err, prob)
		}
		verifySolution(t, prob, sol)
		if sol.DistinctBuckets != oracleBuckets || sol.TotalDelay != oracleDelay {
			t.Fatalf("trial %d: solver (%d buckets, delay %d) disagrees with oracle (%d buckets, delay %d); problem=%+v",
				trial, sol.DistinctBuckets, sol.TotalDelay, oracleBuckets, oracleDelay, prob)
		}
	}
}

func TestRandomLarger(t *testing.T) {
	rng := rand.New(rand.NewSource(4470447))
	for trial := 0; trial < 300; trial++ {
		n := 5 + rng.Intn(14) // 5..18
		prob := randomProblem(rng, n, 12, 8, 0.12)
		sol, err := prob.Solve()
		if err != nil {
			var infeasible *InfeasibleError
			if !errors.As(err, &infeasible) {
				t.Fatalf("trial %d: unexpected error type: %v", trial, err)
			}
			verifyWitness(t, prob, infeasible)
			continue
		}
		verifySolution(t, prob, sol)
	}

	// A fully ordered chain is always feasible with one bucket at the largest
	// occurrence time when every delay window can reach it.
	n := 18
	times := make([]int, n)
	delays := make([]int, n)
	edges := make([][2]int, 0, n-1)
	for i := range times {
		times[i] = i
		delays[i] = n
		if i > 0 {
			edges = append(edges, [2]int{i - 1, i})
		}
	}
	prob := &Problem{Times: times, MaxDelay: delays, Edges: edges}
	sol, err := prob.Solve()
	if err != nil {
		t.Fatalf("chain problem unexpectedly infeasible: %v", err)
	}
	verifySolution(t, prob, sol)
	if sol.DistinctBuckets != 1 {
		t.Fatalf("chain got %d buckets, want 1", sol.DistinctBuckets)
	}
}

func TestDeterminism(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	prob := randomProblem(rng, 12, 10, 6, 0.2)
	first, err := prob.Solve()
	firstErr := err
	for run := 0; run < 5; run++ {
		next, err := prob.Solve()
		if (err == nil) != (firstErr == nil) {
			t.Fatalf("retry %d feasibility flipped: %v vs %v", run, err, firstErr)
		}
		if err != nil {
			if err.Error() != firstErr.Error() {
				t.Fatalf("retry %d error changed: %v vs %v", run, err, firstErr)
			}
			continue
		}
		if !equalIntSlices(first.Buckets, next.Buckets) ||
			first.DistinctBuckets != next.DistinctBuckets ||
			first.TotalDelay != next.TotalDelay {
			t.Fatalf("non-deterministic output: %+v vs %+v", first, next)
		}
	}
}

func TestEdgeCases(t *testing.T) {
	// Self-loops must never constrain an event against itself.
	sol, err := Solve([]int{3, 8}, []int{0, 0}, [][2]int{{0, 0}, {1, 1}})
	if err != nil {
		t.Fatalf("self-loops should be harmless: %v", err)
	}
	verifySolution(t, &Problem{
		Times:    []int{3, 8},
		MaxDelay: []int{0, 0},
		Edges:    [][2]int{{0, 0}, {1, 1}},
	}, sol)
	if sol.DistinctBuckets != 2 {
		t.Fatalf("zero-delay distinct times need two buckets, got %d", sol.DistinctBuckets)
	}

	// Events occurring at the same time collapse into one bucket for free.
	sol, err = Solve([]int{4, 4, 4}, []int{0, 0, 0}, [][2]int{{0, 1}, {1, 2}, {2, 0}})
	if err != nil {
		t.Fatalf("same-time cycle should be feasible: %v", err)
	}
	if sol.DistinctBuckets != 1 || sol.TotalDelay != 0 {
		t.Fatalf("unexpected solution: %+v", sol)
	}
	verifySolution(t, &Problem{
		Times:    []int{4, 4, 4},
		MaxDelay: []int{0, 0, 0},
		Edges:    [][2]int{{0, 1}, {1, 2}, {2, 0}},
	}, sol)

	// Direct infeasibility without a dependency chain: the event's own window.
	_, err = Solve([]int{5}, []int{0}, nil)
	if err != nil {
		t.Fatalf("zero-delay single event must be feasible: %v", err)
	}
	_, err = Solve([]int{9, 0}, []int{0, 2}, [][2]int{{0, 1}})
	var infeasible *InfeasibleError
	if !errors.As(err, &infeasible) {
		t.Fatalf("expected *InfeasibleError, got %v", err)
	}
	verifyWitness(t, &Problem{
		Times:    []int{9, 0},
		MaxDelay: []int{0, 2},
		Edges:    [][2]int{{0, 1}},
	}, infeasible)
}

func TestExhaustiveGrid(t *testing.T) {
	// Systematically sweep every edge configuration on small event sets and
	// cross-check the DP against the brute-force oracle over small domains.
	rng := rand.New(rand.NewSource(77777))
	for n := 2; n <= 3; n++ {
		possibleEdges := make([][2]int, 0, n*n)
		for u := 0; u < n; u++ {
			for v := 0; v < n; v++ {
				possibleEdges = append(possibleEdges, [2]int{u, v})
			}
		}
		for mask := 0; mask < 1<<uint(len(possibleEdges)); mask++ {
			edges := make([][2]int, 0)
			for bit, edge := range possibleEdges {
				if mask&(1<<uint(bit)) != 0 {
					edges = append(edges, edge)
				}
			}
			for trial := 0; trial < 6; trial++ {
				times := make([]int, n)
				delays := make([]int, n)
				for i := range times {
					times[i] = rng.Intn(4)
					delays[i] = rng.Intn(4)
				}
				prob := &Problem{Times: times, MaxDelay: delays, Edges: edges}
				sol, err := prob.Solve()
				oracleBuckets, oracleDelay, feasible := bruteForce(prob)
				if !feasible {
					var infeasible *InfeasibleError
					if !errors.As(err, &infeasible) {
						t.Fatalf("expected infeasible for %+v, got %v", prob, sol)
					}
					verifyWitness(t, prob, infeasible)
					continue
				}
				if err != nil {
					t.Fatalf("unexpected error: %v for %+v", err, prob)
				}
				verifySolution(t, prob, sol)
				if sol.DistinctBuckets != oracleBuckets || sol.TotalDelay != oracleDelay {
					t.Fatalf("oracle mismatch on %+v: got (%d,%d) want (%d,%d)",
						prob, sol.DistinctBuckets, sol.TotalDelay, oracleBuckets, oracleDelay)
				}
			}
		}
	}
}

func BenchmarkSolve18(b *testing.B) {
	rng := rand.New(rand.NewSource(18018))
	prob := randomProblem(rng, 18, 20, 10, 0.08)
	// Loosen every window to the full time span so the benchmark instance is
	// guaranteed feasible while retaining the full subset-DP search space.
	for i := range prob.MaxDelay {
		prob.MaxDelay[i] = 20
	}
	if _, err := prob.Solve(); err != nil {
		b.Fatalf("benchmark problem infeasible: %v", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := prob.Solve(); err != nil {
			b.Fatal(err)
		}
	}
}

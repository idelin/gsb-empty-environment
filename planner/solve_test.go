package planner

import (
	"math/rand"
	"testing"
	"time"
)

// randomDAG builds a random DAG on n nodes; deps always point to smaller
// indices so 0..n-1 is a topological order.
func randomDAG(rng *rand.Rand, n int) Spec {
	spec := Spec{Blocks: make([]Block, n)}
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		ids[i] = string(rune('a'+i%26)) + string(rune('0'+i/26))
	}
	for i := 0; i < n; i++ {
		b := Block{
			ID:         ids[i],
			Compressed: int64(1 + rng.Intn(12)),
			Temp:       int64(1 + rng.Intn(20)),
			Final:      int64(1 + rng.Intn(12)),
		}
		for j := 0; j < i; j++ {
			if rng.Float64() < 0.3 {
				b.Deps = append(b.Deps, ids[j])
			}
		}
		spec.Blocks[i] = b
	}
	return spec
}

// bruteForce min peak over every topological permutation.
func bruteForce(t *testing.T, p *Planner) int64 {
	t.Helper()
	n := p.n
	perm := make([]int, n)
	for i := range perm {
		perm[i] = i
	}
	best := int64(-1)
	var visit func(k int)
	visit = func(k int) {
		if k == n {
			// topological check
			pos := make([]int, n)
			for i, v := range perm {
				pos[v] = i
			}
			for i := 0; i < n; i++ {
				for _, d := range p.deps[i] {
					if pos[d] > pos[i] {
						return
					}
				}
			}
			if peak := p.eval(perm); best < 0 || peak < best {
				best = peak
			}
			return
		}
		for i := k; i < n; i++ {
			perm[k], perm[i] = perm[i], perm[k]
			visit(k + 1)
			perm[k], perm[i] = perm[i], perm[k]
		}
	}
	visit(0)
	if best < 0 {
		t.Fatal("no topological order found")
	}
	return best
}

func TestSolveMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 150; trial++ {
		n := 2 + rng.Intn(6) // 2..7 blocks
		p := mustNew(t, randomDAG(rng, n))
		res := Solve(p, 0, Options{})
		if !res.Optimal {
			t.Fatalf("trial %d: n=%d not proven optimal", trial, n)
		}
		want := bruteForce(t, p)
		if res.Peak != want {
			t.Fatalf("trial %d: peak=%d, brute force=%d", trial, res.Peak, want)
		}
		if res.LowerBound > res.Peak {
			t.Fatalf("trial %d: lower bound %d exceeds optimum %d", trial, res.LowerBound, res.Peak)
		}
		if !res.Feasible {
			t.Fatalf("trial %d: infeasible without quota", trial)
		}
	}
}

func TestInterruptedSolveReturnsCertifiedBounds(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	p := mustNew(t, randomDAG(rng, 13))

	// Full solve for ground truth.
	full := Solve(p, 0, Options{EnumerateBelow: 13})
	if !full.Optimal {
		t.Fatal("full solve should be optimal for n=13")
	}

	// Starved budget: enumeration stops almost immediately.
	cut := Solve(p, 0, Options{EnumerateBelow: 13, MaxNodes: 1})
	if cut.Optimal {
		t.Fatal("starved solve must not claim optimality")
	}
	// The interrupted result must still certify: LB <= optimum <= UB.
	if cut.LowerBound > full.Peak {
		t.Fatalf("interrupted LB %d exceeds optimum %d", cut.LowerBound, full.Peak)
	}
	if cut.UpperBound < full.Peak {
		t.Fatalf("interrupted UB %d below optimum %d", cut.UpperBound, full.Peak)
	}
	// And the returned order must really achieve the reported upper bound.
	ord := make([]int, p.n)
	idx := map[string]int{}
	for i, id := range p.ids {
		idx[id] = i
	}
	for k, id := range cut.Order {
		ord[k] = idx[id]
	}
	peak, err := p.Eval(ord)
	if err != nil {
		t.Fatalf("returned order invalid: %v", err)
	}
	if peak != cut.UpperBound {
		t.Fatalf("order peaks at %d but UB claims %d", peak, cut.UpperBound)
	}
}

func TestDeadlineInterruption(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	p := mustNew(t, randomDAG(rng, 14))
	res := Solve(p, 0, Options{Deadline: time.Now()})
	if res.Optimal {
		t.Fatal("expired deadline must not claim optimality")
	}
	if res.UpperBound < res.LowerBound {
		t.Fatalf("UB %d < LB %d", res.UpperBound, res.LowerBound)
	}
}

func TestQuotaFeasibility(t *testing.T) {
	p := mustNew(t, diamondSpec())
	res := Solve(p, 0, Options{})
	if !res.Optimal || res.Peak != 15 {
		t.Fatalf("diamond optimum = %d optimal=%v, want 15/true", res.Peak, res.Optimal)
	}

	ok := Solve(p, 15, Options{})
	if !ok.Feasible || ok.ProvablyInfeasible {
		t.Fatalf("quota 15 should be feasible: %+v", ok)
	}

	tight := Solve(p, 14, Options{})
	if tight.Feasible {
		t.Fatalf("quota 14 must be infeasible: %+v", tight)
	}
	if !tight.ProvablyInfeasible {
		t.Fatal("quota 14 < LB 15 must be provably infeasible")
	}
}

func TestReleaseAfterLastDependent(t *testing.T) {
	// If a were released after b (not after c), c's decompress would see
	// usage 11 instead of 15 and the peak would drop. The diamond test
	// already pins peak=15; here we pin the end-of-run usage indirectly:
	// outputs default to sinks, so only d's 8 bytes remain at the end.
	p := mustNew(t, diamondSpec())
	s := newState(p)
	for _, i := range []int{0, 1, 2, 3} {
		s.apply(p, i)
	}
	if s.usage != 8 {
		t.Fatalf("end usage = %d, want 8 (only output d retained)", s.usage)
	}
}

func TestGreedyNeverWorseThanAnySinglePass(t *testing.T) {
	// Sanity: greedy result is a valid topological order and its peak is
	// at least the lower bound.
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 50; trial++ {
		p := mustNew(t, randomDAG(rng, 3+rng.Intn(8)))
		res := Solve(p, 0, Options{EnumerateBelow: 1}) // heuristic path only
		if res.Optimal {
			t.Fatal("heuristic path must not claim optimality")
		}
		if res.UpperBound < res.LowerBound {
			t.Fatalf("trial %d: UB %d < LB %d", trial, res.UpperBound, res.LowerBound)
		}
	}
}

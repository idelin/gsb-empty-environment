package main

import (
	"fmt"
	"math/big"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// bruteForce enumerates all 2^n file subsets (test-only; n <= 15 here) and
// returns the minimum-cost feasible plan using the same tie-break rule as
// the solver. Feasibility is decided by the independent verifier.
func bruteForce(in *Input) (cost *big.Int, files []string, ok bool) {
	n := len(in.Files)
	if n > 15 {
		panic("brute force limited to 15 files")
	}
	ids := make([]string, 0, n)
	for _, f := range in.Files {
		ids = append(ids, f.ID)
	}
	sort.Strings(ids)
	var best *big.Int
	var bestIDs []string
	for mask := uint64(0); mask < uint64(1)<<uint(n); mask++ {
		var sel []string
		for i := 0; i < n; i++ {
			if mask&(uint64(1)<<uint(i)) != 0 {
				sel = append(sel, ids[i])
			}
		}
		ver := Verify(in, sel)
		if !ver.OK {
			continue
		}
		c, _ := new(big.Int).SetString(ver.Cost, 10)
		if best == nil || c.Cmp(best) < 0 || (c.Cmp(best) == 0 && lessIDs(sel, bestIDs)) {
			best = c
			bestIDs = sel
		}
	}
	if best == nil {
		return nil, nil, false
	}
	return best, bestIDs, true
}

// genInstance builds a deterministic pseudo-random instance with at most
// maxFiles files. Byte costs are >= 1 so that every minimum-cost feasible
// set is exactly a union of path closures (zero-cost extras would make the
// all-subsets tie-break ill-defined for the solver's search space; zero
// costs are covered by dedicated tests).
func genInstance(r *rand.Rand, maxFiles int) *Input {
	n := 1 + r.Intn(maxFiles)
	in := &Input{}
	for i := 0; i < n; i++ {
		intact := r.Intn(100) < 85
		f := file(fmt.Sprintf("f%02d", i), fmt.Sprint(1+r.Intn(20)), intact)
		for j := 0; j < i; j++ {
			if r.Intn(100) < 20 {
				f.Deps = append(f.Deps, fmt.Sprintf("f%02d", j))
			}
		}
		in.Files = append(in.Files, f)
	}
	numShards := 1 + r.Intn(3)
	for s := 0; s < numShards; s++ {
		versions := int64(1 + r.Intn(4))
		sp := shard(fmt.Sprintf("s%d", s), versions)
		for v := int64(0); v < versions; v++ {
			k := 1 + r.Intn(2)
			for e := 0; e < k; e++ {
				sp.Edges = append(sp.Edges, ed(v, v+1, fmt.Sprintf("f%02d", r.Intn(n))))
			}
			if v+2 <= versions && r.Intn(100) < 30 {
				sp.Edges = append(sp.Edges, ed(v, v+2, fmt.Sprintf("f%02d", r.Intn(n))))
			}
		}
		in.Shards = append(in.Shards, sp)
	}
	return in
}

// Cross-check the exact solver against full subset enumeration on instances
// with at most 15 files, and validate every solver plan with the
// independent verifier.
func TestExhaustiveCrossCheck(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for trial := 0; trial < 40; trial++ {
		in := genInstance(r, 15)
		wantCost, wantFiles, feasible := bruteForce(in)

		m, diags, fatal := buildModel(in)
		if fatal {
			t.Fatalf("trial %d: unexpected fatal diagnostics %+v", trial, diags)
		}
		sol := Solve(m, diags, -1)

		if !feasible {
			if sol.Status != StatusInfeasible {
				t.Fatalf("trial %d: status=%s, want infeasible", trial, sol.Status)
			}
			continue
		}
		if sol.Status != StatusOptimal {
			t.Fatalf("trial %d: status=%s, want optimal", trial, sol.Status)
		}
		if sol.Cost != wantCost.String() {
			t.Fatalf("trial %d: cost=%s, brute force=%s", trial, sol.Cost, wantCost)
		}
		if !reflect.DeepEqual(sol.Files, wantFiles) {
			t.Fatalf("trial %d: files=%v, brute force=%v", trial, sol.Files, wantFiles)
		}
		if v := Verify(in, sol.Files); !v.OK || v.Cost != sol.Cost {
			t.Fatalf("trial %d: verifier disagrees: %+v", trial, v)
		}
	}
}

// With a limited budget the solver must never claim optimality unless the
// search completed, and its lower bound must never exceed the true optimum.
func TestBudgetedRunsStayConsistent(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	budgets := []int64{0, 1, 3, 7, 50}
	for trial := 0; trial < 25; trial++ {
		in := genInstance(r, 12)
		wantCost, _, feasible := bruteForce(in)
		for _, b := range budgets {
			m, diags, fatal := buildModel(in)
			if fatal {
				t.Fatalf("trial %d: fatal diagnostics %+v", trial, diags)
			}
			sol := Solve(m, diags, b)
			if sol.Expansions > b {
				t.Fatalf("trial %d budget %d: expansions %d exceed budget", trial, b, sol.Expansions)
			}
			switch sol.Status {
			case StatusOptimal:
				if !feasible || sol.Cost != wantCost.String() {
					t.Fatalf("trial %d budget %d: wrong optimum %s", trial, b, sol.Cost)
				}
			case StatusFeasible:
				got, _ := new(big.Int).SetString(sol.Cost, 10)
				if !feasible || got.Cmp(wantCost) < 0 {
					t.Fatalf("trial %d budget %d: feasible cost %s beats brute optimum", trial, b, sol.Cost)
				}
				fallthrough
			case StatusUnknown:
				lb, _ := new(big.Int).SetString(sol.LowerBound, 10)
				if feasible && lb.Cmp(wantCost) > 0 {
					t.Fatalf("trial %d budget %d: lower bound %s exceeds optimum %s", trial, b, lb, wantCost)
				}
				if sol.Status == StatusUnknown && sol.Files != nil {
					t.Fatalf("trial %d budget %d: unknown status with files", trial, b)
				}
			case StatusInfeasible:
				if feasible {
					t.Fatalf("trial %d budget %d: infeasible but brute force found %s", trial, b, wantCost)
				}
			}
		}
	}
}

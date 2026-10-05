package main

import (
	"fmt"
	"math/big"
	"math/rand"
	"reflect"
	"testing"
)

// randomInput builds a deterministic pseudo-random instance with at most 15
// files: acyclic dependencies (only to earlier files), random zero costs,
// random corruption, and 1-2 shards with random increasing version edges.
func randomInput(r *rand.Rand) *Input {
	n := 3 + r.Intn(13) // 3..15 files
	in := &Input{}
	for i := 0; i < n; i++ {
		f := FileInput{ID: fmt.Sprintf("f%02d", i)}
		if r.Intn(4) == 0 {
			f.Bytes = nat(0)
		} else {
			f.Bytes = nat(int64(1 + r.Intn(50)))
		}
		for j := 0; j < i; j++ {
			if r.Intn(5) == 0 {
				f.Dependencies = append(f.Dependencies, fmt.Sprintf("f%02d", j))
			}
		}
		f.Corrupted = r.Intn(10) == 0
		in.Files = append(in.Files, f)
	}
	numShards := 1 + r.Intn(2)
	for s := 0; s < numShards; s++ {
		sh := ShardInput{Name: fmt.Sprintf("shard%d", s)}
		versions := []int64{0}
		cur := int64(0)
		for k := 0; k < 2+r.Intn(4); k++ {
			cur += 1 + int64(r.Intn(5))
			versions = append(versions, cur)
		}
		sh.Target = cur
		for a := 0; a < len(versions); a++ {
			for b := a + 1; b < len(versions); b++ {
				if r.Intn(3) == 0 {
					sh.Edges = append(sh.Edges, EdgeInput{
						From: versions[a], To: versions[b],
						File: fmt.Sprintf("f%02d", r.Intn(n)),
					})
				}
			}
		}
		in.Shards = append(in.Shards, sh)
	}
	return in
}

// TestBruteForceCrossCheck compares the solver against full subset
// enumeration on instances with at most 15 files.
func TestBruteForceCrossCheck(t *testing.T) {
	r := rand.New(rand.NewSource(20261005))
	feasibleSeen := 0
	for trial := 0; trial < 400; trial++ {
		in := randomInput(r)
		env, errs := buildEnv(in)
		if len(errs) > 0 {
			t.Fatalf("trial %d: unexpected input errors: %+v", trial, errs)
		}
		wantCost, wantIDs, wantOK := bruteForce(env)
		out := solve(in, -1)
		if !wantOK {
			if out.Status != StatusInfeasible {
				t.Fatalf("trial %d: status = %s, want infeasible", trial, out.Status)
			}
			continue
		}
		feasibleSeen++
		if out.Status != StatusOptimal {
			t.Fatalf("trial %d: status = %s, want optimal", trial, out.Status)
		}
		if out.Solution.TotalBytes != wantCost.String() {
			t.Fatalf("trial %d: cost %s, brute force says %s", trial, out.Solution.TotalBytes, wantCost)
		}
		if !reflect.DeepEqual(out.Solution.Files, wantIDs) {
			t.Fatalf("trial %d: files %v, brute force says %v", trial, out.Solution.Files, wantIDs)
		}
		if out.LowerBound != wantCost.String() {
			t.Fatalf("trial %d: lower bound %s != optimum %s", trial, out.LowerBound, wantCost)
		}
	}
	if feasibleSeen == 0 {
		t.Fatal("no feasible instances generated; cross-check is vacuous")
	}
}

// TestBudgetedRunsStayConsistent: under any budget the solver must either
// prove the true optimum or honestly report budget_exhausted with a valid
// lower bound and a feasible (verified) solution.
func TestBudgetedRunsStayConsistent(t *testing.T) {
	r := rand.New(rand.NewSource(777))
	for trial := 0; trial < 300; trial++ {
		in := randomInput(r)
		env, errs := buildEnv(in)
		if len(errs) > 0 {
			t.Fatalf("trial %d: unexpected input errors: %+v", trial, errs)
		}
		wantCost, _, wantOK := bruteForce(env)
		out := solve(in, int64(r.Intn(40)))
		switch out.Status {
		case StatusOptimal:
			if !wantOK || out.Solution.TotalBytes != wantCost.String() {
				t.Fatalf("trial %d: claimed optimal %v but brute force says %v (ok=%v)",
					trial, out.Solution, wantCost, wantOK)
			}
		case StatusInfeasible:
			if wantOK {
				t.Fatalf("trial %d: claimed infeasible but brute force found cost %s", trial, wantCost)
			}
		case StatusBudgetExhausted:
			lb, ok := new(big.Int).SetString(out.LowerBound, 10)
			if !ok || lb.Sign() < 0 {
				t.Fatalf("trial %d: bad lower bound %q", trial, out.LowerBound)
			}
			if wantOK && lb.Cmp(wantCost) > 0 {
				t.Fatalf("trial %d: lower bound %s exceeds true optimum %s", trial, lb, wantCost)
			}
			if out.Solution != nil {
				vout := verify(&VerifyRequest{Input: *in, Solution: *out.Solution})
				if !vout.Valid {
					t.Fatalf("trial %d: budgeted solution failed verification: %s", trial, vout.Reason)
				}
				got, _ := new(big.Int).SetString(out.Solution.TotalBytes, 10)
				if got.Cmp(lb) < 0 {
					t.Fatalf("trial %d: solution cost %s below lower bound %s", trial, got, lb)
				}
			}
		default:
			t.Fatalf("trial %d: unexpected status %s", trial, out.Status)
		}
	}
}

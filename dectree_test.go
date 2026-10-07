package dectree

import (
	"fmt"
	"math/big"
	"math/rand"
	"strings"
	"testing"
)

func rat(num, den int64) *big.Rat { return big.NewRat(num, den) }

func cands(probs ...*big.Rat) []Candidate {
	out := make([]Candidate, len(probs))
	for i, p := range probs {
		out[i] = Candidate{Name: fmt.Sprintf("c%d", i), Prob: p}
	}
	return out
}

// lexicographicProblem builds the 3-candidate scenario:
//
//	split0  cost 2: {c0} | {c1,c2}
//	split12 cost 3: {c0,c1} | {c2}
//	all3    cost allCost: {c0} | {c1} | {c2}
func lexicographicProblem(allCost int) *Problem {
	return &Problem{
		Candidates: cands(rat(1, 2), rat(1, 4), rat(1, 4)),
		Tests: []Test{
			{Name: "split0", Cost: 2, Outcome: []string{"x", "y", "y"}},
			{Name: "split12", Cost: 3, Outcome: []string{"z", "z", "w"}},
			{Name: "all3", Cost: allCost, Outcome: []string{"a", "b", "c"}},
		},
		Budget: 100,
	}
}

func TestWorstCaseDominatesExpected(t *testing.T) {
	// all3 alone: worst 4, expected 4. Any two-test plan costs 5 in the
	// worst case, so the single expensive test wins on the first criterion.
	sol, err := Solve(lexicographicProblem(4))
	if err != nil {
		t.Fatal(err)
	}
	if sol.WorstCost != 4 || sol.Expected.Cmp(rat(4, 1)) != 0 {
		t.Fatalf("got worst=%d expected=%s, want 4 and 4", sol.WorstCost, sol.Expected)
	}
	if sol.Root.Test != 2 {
		t.Fatalf("root test = %d, want all3 (2)", sol.Root.Test)
	}
	if err := Verify(lexicographicProblem(4), sol); err != nil {
		t.Fatal(err)
	}
}

func TestExpectedBreaksWorstCaseTie(t *testing.T) {
	// Every full identification now has worst cost 5:
	//   all3 alone:        expected 5
	//   split0 then split12: expected 2 + 3*(1/2) = 7/2   <- optimal
	//   split12 then split0: expected 3 + 2*(3/4) = 9/2
	p := lexicographicProblem(5)
	sol, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if sol.WorstCost != 5 || sol.Expected.Cmp(rat(7, 2)) != 0 {
		t.Fatalf("got worst=%d expected=%s, want 5 and 7/2", sol.WorstCost, sol.Expected)
	}
	if sol.Root.Test != 0 {
		t.Fatalf("root test = %d, want split0 (0)", sol.Root.Test)
	}
	if err := Verify(p, sol); err != nil {
		t.Fatal(err)
	}
}

func TestIndistinguishableReportedWithEvidence(t *testing.T) {
	p := &Problem{
		Candidates: cands(rat(1, 3), rat(1, 3), rat(1, 3)),
		Tests: []Test{
			{Name: "probe", Cost: 1, Outcome: []string{"same", "same", "other"}},
		},
		Budget: 10,
	}
	sol, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(sol.Ambiguous) != 1 {
		t.Fatalf("got %d ambiguous groups, want 1", len(sol.Ambiguous))
	}
	g := sol.Ambiguous[0]
	if fmt.Sprint(g.Candidates) != "[0 1]" {
		t.Fatalf("ambiguous candidates = %v, want [0 1]", g.Candidates)
	}
	if len(g.Evidence) != 1 || g.Evidence[0].Test != 0 || g.Evidence[0].Outcome != "same" {
		t.Fatalf("evidence = %+v, want [{0 same}]", g.Evidence)
	}
	if err := Verify(p, sol); err != nil {
		t.Fatal(err)
	}
}

func TestBudgetExhaustion(t *testing.T) {
	p := &Problem{
		Candidates: cands(rat(1, 2), rat(1, 2)),
		Tests:      []Test{{Name: "coin", Cost: 3, Outcome: []string{"h", "t"}}},
		Budget:     2,
	}
	sol, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if sol.WorstCost != 0 || len(sol.Ambiguous) != 1 || len(sol.Ambiguous[0].Candidates) != 2 {
		t.Fatalf("got worst=%d ambiguous=%+v, want unseparated pair at cost 0", sol.WorstCost, sol.Ambiguous)
	}
	if len(sol.Ambiguous[0].Evidence) != 0 {
		t.Fatalf("evidence = %+v, want empty at the root", sol.Ambiguous[0].Evidence)
	}
}

func TestSingleCandidate(t *testing.T) {
	p := &Problem{
		Candidates: cands(rat(1, 1)),
		Tests:      []Test{{Name: "noop", Cost: 1, Outcome: []string{"x"}}},
		Budget:     5,
	}
	sol, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if sol.WorstCost != 0 || !sol.Root.IsLeaf() || len(sol.Root.Leaf) != 1 {
		t.Fatalf("got %+v, want immediate leaf", sol)
	}
}

func TestVerifyRejectsSuboptimal(t *testing.T) {
	p := lexicographicProblem(5)
	// split12 first, then split0: valid but expected 9/2 > 7/2.
	root := &Node{Test: 1, Branches: map[string]*Node{
		"z": {Test: 0, Branches: map[string]*Node{
			"x": {Test: -1, Leaf: []int{0}},
			"y": {Test: -1, Leaf: []int{1}},
		}},
		"w": {Test: -1, Leaf: []int{2}},
	}}
	sol := &Solution{Root: root, WorstCost: 5, Expected: rat(9, 2)}
	err := Verify(p, sol)
	if err == nil || !strings.Contains(err.Error(), "not optimal") {
		t.Fatalf("err = %v, want optimality failure", err)
	}
}

func TestVerifyRejectsDistinguishableLeaf(t *testing.T) {
	p := lexicographicProblem(5)
	root := &Node{Test: 0, Branches: map[string]*Node{
		"x": {Test: -1, Leaf: []int{0}},
		"y": {Test: -1, Leaf: []int{1, 2}}, // split12 can tell these apart
	}}
	sol := &Solution{Root: root, WorstCost: 2, Expected: rat(2, 1)}
	err := Verify(p, sol)
	if err == nil || !strings.Contains(err.Error(), "separable") {
		t.Fatalf("err = %v, want separability failure", err)
	}
}

func TestVerifyRejectsMissingBranch(t *testing.T) {
	p := lexicographicProblem(5)
	// A "strategy" that only handles outcome x could only work by knowing
	// the truth in advance; the verifier must reject it.
	root := &Node{Test: 0, Branches: map[string]*Node{
		"x": {Test: -1, Leaf: []int{0}},
	}}
	sol := &Solution{Root: root, WorstCost: 2, Expected: rat(2, 1)}
	if err := Verify(p, sol); err == nil {
		t.Fatal("want error for missing branch")
	}
}

func TestVerifyRejectsOverBudget(t *testing.T) {
	p := lexicographicProblem(5)
	p.Budget = 4
	root := &Node{Test: 2, Branches: map[string]*Node{
		"a": {Test: -1, Leaf: []int{0}},
		"b": {Test: -1, Leaf: []int{1}},
		"c": {Test: -1, Leaf: []int{2}},
	}}
	sol := &Solution{Root: root, WorstCost: 5, Expected: rat(5, 1)}
	err := Verify(p, sol)
	if err == nil || !strings.Contains(err.Error(), "exceeds budget") {
		t.Fatalf("err = %v, want budget failure", err)
	}
}

func TestValidation(t *testing.T) {
	if _, err := Solve(&Problem{Candidates: cands()}); err == nil {
		t.Fatal("want error for zero candidates")
	}
	nine := make([]Candidate, 9)
	for i := range nine {
		nine[i] = Candidate{Prob: rat(1, 9)}
	}
	if _, err := Solve(&Problem{Candidates: nine}); err == nil {
		t.Fatal("want error for more than 8 candidates")
	}
	if _, err := Solve(&Problem{
		Candidates: cands(rat(1, 2), rat(1, 2)),
		Tests:      []Test{{Name: "bad", Cost: 1, Outcome: []string{"x"}}},
	}); err == nil {
		t.Fatal("want error for wrong outcome count")
	}
}

// TestRandomizedCrossCheck compares the memoized solver against brute-force
// enumeration of all strategy trees, and runs the full verifier, on random
// instances.
func TestRandomizedCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 300; trial++ {
		n := 2 + rng.Intn(5) // 2..6 candidates
		probs := make([]*big.Rat, n)
		for i := range probs {
			probs[i] = rat(int64(1+rng.Intn(5)), int64(1+rng.Intn(3)))
		}
		nt := 1 + rng.Intn(4)
		tests := make([]Test, nt)
		for j := range tests {
			alphabet := 1 + rng.Intn(3)
			outcomes := make([]string, n)
			for i := range outcomes {
				outcomes[i] = string(rune(97 + rng.Intn(alphabet))) // 97 = a
			}
			tests[j] = Test{
				Name:    fmt.Sprintf("t%d", j),
				Cost:    rng.Intn(5),
				Outcome: outcomes,
			}
		}
		p := &Problem{Candidates: cands(probs...), Tests: tests, Budget: rng.Intn(9)}

		sol, err := Solve(p)
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		full := uint16(1<<uint(n)) - 1
		wantWorst, wantExp := exhaustiveOptimum(p, full, p.Budget)
		if sol.WorstCost != wantWorst || sol.Expected.Cmp(wantExp) != 0 {
			t.Fatalf("trial %d: solver=(%d,%s) exhaustive=(%d,%s)",
				trial, sol.WorstCost, sol.Expected, wantWorst, wantExp)
		}
		if err := Verify(p, sol); err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		// Every candidate must reach a leaf containing it, within budget.
		for i := 0; i < n; i++ {
			cost, leaf, err := Simulate(p, sol.Root, i)
			if err != nil {
				t.Fatalf("trial %d: %v", trial, err)
			}
			if cost > p.Budget {
				t.Fatalf("trial %d: candidate %d paid %d > budget %d", trial, i, cost, p.Budget)
			}
			found := false
			for _, c := range leaf {
				if c == i {
					found = true
				}
			}
			if !found {
				t.Fatalf("trial %d: candidate %d landed in leaf %v", trial, i, leaf)
			}
		}
	}
}

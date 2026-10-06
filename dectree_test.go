package dectree

import (
	"math/big"
	"strings"
	"testing"
)

func rat(n, d int64) *big.Rat { return big.NewRat(n, d) }

// Three candidates; each test singles out one candidate. With
// X twice as likely as Y or Z, testing for X first keeps the same
// worst-case cost (2) but lowers the expected cost (3/2 vs 7/4).
func tieBreakInstance() *Instance {
	return &Instance{
		Candidates: []Candidate{
			{ID: "X", Prob: rat(1, 2)},
			{ID: "Y", Prob: rat(1, 4)},
			{ID: "Z", Prob: rat(1, 4)},
		},
		Tests: []Test{
			{Name: "tX", Cost: 1, Outcomes: map[string]string{"X": "yes", "Y": "no", "Z": "no"}},
			{Name: "tY", Cost: 1, Outcomes: map[string]string{"X": "no", "Y": "yes", "Z": "no"}},
			{Name: "tZ", Cost: 1, Outcomes: map[string]string{"X": "no", "Y": "no", "Z": "yes"}},
		},
		Budget: 2,
	}
}

func TestSolveMinimisesWorstThenExpected(t *testing.T) {
	root, err := Solve(tieBreakInstance())
	if err != nil {
		t.Fatal(err)
	}
	if root.Test != "tX" {
		t.Fatalf("root test = %q, want %q (tie on worst cost, lower expected cost)", root.Test, "tX")
	}
	if root.Worst != 2 {
		t.Fatalf("worst = %d, want 2", root.Worst)
	}
	if root.Exp.Cmp(rat(3, 2)) != 0 {
		t.Fatalf("expected cost = %s, want 3/2", root.Exp)
	}
	for _, leaf := range root.Leaves() {
		if len(leaf.Candidates) != 1 {
			t.Fatalf("leaf %v not fully identified", leaf.Candidates)
		}
		if leaf.PathCost > 2 {
			t.Fatalf("leaf %v exceeds budget", leaf.Candidates)
		}
	}
	if err := ExhaustiveVerify(tieBreakInstance(), root); err != nil {
		t.Fatal(err)
	}
}

// P and Q yield identical outcomes on every test: indistinguishable.
func indistinguishableInstance() *Instance {
	return &Instance{
		Candidates: []Candidate{
			{ID: "P", Prob: rat(1, 3)},
			{ID: "Q", Prob: rat(1, 3)},
			{ID: "R", Prob: rat(1, 3)},
		},
		Tests: []Test{
			{Name: "t1", Cost: 1, Outcomes: map[string]string{"P": "a", "Q": "a", "R": "b"}},
		},
		Budget: 5,
	}
}

func TestIndistinguishableReportedWithEvidence(t *testing.T) {
	inst := indistinguishableInstance()
	root, err := Solve(inst)
	if err != nil {
		t.Fatal(err)
	}
	leaves := root.Leaves()
	if len(leaves) != 2 {
		t.Fatalf("got %d leaves, want 2", len(leaves))
	}
	var pq *Strategy
	for _, l := range leaves {
		if len(l.Candidates) == 2 {
			pq = l
		}
	}
	if pq == nil {
		t.Fatal("no leaf reports the indistinguishable pair")
	}
	if pq.Candidates[0] != "P" || pq.Candidates[1] != "Q" {
		t.Fatalf("indistinguishable leaf = %v, want [P Q]", pq.Candidates)
	}
	if len(pq.Evidence) != 1 || pq.Evidence[0] != (Step{Test: "t1", Outcome: "a"}) {
		t.Fatalf("evidence = %v, want [{t1 a}]", pq.Evidence)
	}
	if err := ExhaustiveVerify(inst, root); err != nil {
		t.Fatal(err)
	}
}

func TestBudgetExhaustionReportsWholeSet(t *testing.T) {
	inst := indistinguishableInstance()
	inst.Budget = 0
	root, err := Solve(inst)
	if err != nil {
		t.Fatal(err)
	}
	if !root.Leaf {
		t.Fatal("with zero budget the root must be a leaf")
	}
	if len(root.Candidates) != 3 {
		t.Fatalf("leaf = %v, want all three candidates", root.Candidates)
	}
	if len(root.Evidence) != 0 {
		t.Fatalf("evidence = %v, want empty", root.Evidence)
	}
	if err := ExhaustiveVerify(inst, root); err != nil {
		t.Fatal(err)
	}
}

// Four candidates, two binary tests of cost 1, budget 2: every path
// pays exactly 2, so worst = expected = 2 and all leaves are singletons.
func TestFourCandidates(t *testing.T) {
	inst := &Instance{
		Candidates: []Candidate{
			{ID: "A", Prob: rat(1, 2)},
			{ID: "B", Prob: rat(1, 4)},
			{ID: "C", Prob: rat(1, 8)},
			{ID: "D", Prob: rat(1, 8)},
		},
		Tests: []Test{
			{Name: "t1", Cost: 1, Outcomes: map[string]string{"A": "x", "B": "x", "C": "y", "D": "y"}},
			{Name: "t2", Cost: 1, Outcomes: map[string]string{"A": "p", "B": "q", "C": "p", "D": "q"}},
		},
		Budget: 2,
	}
	root, err := Solve(inst)
	if err != nil {
		t.Fatal(err)
	}
	if root.Worst != 2 || root.Exp.Cmp(rat(2, 1)) != 0 {
		t.Fatalf("worst=%d exp=%s, want 2 and 2", root.Worst, root.Exp)
	}
	if len(root.Leaves()) != 4 {
		t.Fatalf("got %d leaves, want 4", len(root.Leaves()))
	}
	if err := ExhaustiveVerify(inst, root); err != nil {
		t.Fatal(err)
	}
}

func TestSimulateFollowsObservationsOnly(t *testing.T) {
	inst := tieBreakInstance()
	root, err := Solve(inst)
	if err != nil {
		t.Fatal(err)
	}
	leaf, cost, err := Simulate(inst, root, "Z")
	if err != nil {
		t.Fatal(err)
	}
	if len(leaf.Candidates) != 1 || leaf.Candidates[0] != "Z" {
		t.Fatalf("truth Z reaches %v", leaf.Candidates)
	}
	if cost != 2 {
		t.Fatalf("cost = %d, want 2", cost)
	}
	// Same observation path for Y and Z after tX=no: the next test is
	// fixed by the outcome, not by the truth.
	if root.Branches["no"].Test == "" {
		t.Fatal("expected an internal node after tX=no")
	}
}

func TestVerifyRejectsSuboptimalStrategy(t *testing.T) {
	inst := tieBreakInstance()
	// Root on tY: worst 2 but expected 7/4 > 3/2.
	bad := &Strategy{
		Test: "tY",
		Branches: map[string]*Strategy{
			"yes": {Leaf: true, Candidates: []string{"Y"}, Evidence: []Step{{"tY", "yes"}}, PathCost: 1},
			"no": {
				Test: "tX",
				Branches: map[string]*Strategy{
					"yes": {Leaf: true, Candidates: []string{"X"}, Evidence: []Step{{"tY", "no"}, {"tX", "yes"}}, PathCost: 2},
					"no":  {Leaf: true, Candidates: []string{"Z"}, Evidence: []Step{{"tY", "no"}, {"tX", "no"}}, PathCost: 2},
				},
				Candidates: []string{"X", "Z"},
				Evidence:   []Step{{"tY", "no"}},
				PathCost:   1,
				Worst:      1,
				Exp:        rat(1, 1),
			},
		},
		Candidates: []string{"X", "Y", "Z"},
		Worst:      2,
		Exp:        rat(7, 4),
	}
	err := ExhaustiveVerify(inst, bad)
	if err == nil || !strings.Contains(err.Error(), "not optimal") {
		t.Fatalf("want optimality failure, got %v", err)
	}
}

func TestVerifyRejectsUnjustifiedLeaf(t *testing.T) {
	inst := tieBreakInstance()
	// Stop immediately although affordable tests can split the set.
	lazy := &Strategy{Leaf: true, Candidates: []string{"X", "Y", "Z"}}
	err := ExhaustiveVerify(inst, lazy)
	if err == nil || !strings.Contains(err.Error(), "unjustified") {
		t.Fatalf("want unjustified-leaf failure, got %v", err)
	}
}

func TestVerifyRejectsMissingBranch(t *testing.T) {
	inst := tieBreakInstance()
	broken := &Strategy{
		Test:       "tX",
		Branches:   map[string]*Strategy{"yes": {Leaf: true, Candidates: []string{"X"}, Evidence: []Step{{"tX", "yes"}}, PathCost: 1}},
		Candidates: []string{"X", "Y", "Z"},
	}
	err := ExhaustiveVerify(inst, broken)
	if err == nil || !strings.Contains(err.Error(), "branch") {
		t.Fatalf("want branch failure, got %v", err)
	}
}

func TestValidate(t *testing.T) {
	nine := &Instance{Budget: 1}
	for i := 0; i < 9; i++ {
		nine.Candidates = append(nine.Candidates, Candidate{ID: string(rune('a' + i)), Prob: rat(1, 9)})
	}
	if err := nine.Validate(); err == nil {
		t.Fatal("want error for 9 candidates")
	}
	missing := &Instance{
		Candidates: []Candidate{{ID: "A", Prob: rat(1, 1)}},
		Tests:      []Test{{Name: "t", Cost: 1, Outcomes: map[string]string{}}},
	}
	if err := missing.Validate(); err == nil {
		t.Fatal("want error for missing outcome")
	}
	neg := &Instance{
		Candidates: []Candidate{{ID: "A", Prob: rat(1, 1)}},
		Budget:     -1,
	}
	if err := neg.Validate(); err == nil {
		t.Fatal("want error for negative budget")
	}
}

func TestEightCandidatesExhaustive(t *testing.T) {
	// Eight candidates, three binary tests forming a full split, plus
	// one expensive shortcut test for the most likely candidate.
	inst := &Instance{Budget: 4}
	outcomes := func(bit uint) map[string]string {
		m := make(map[string]string)
		for i := 0; i < 8; i++ {
			id := string(rune('A' + i))
			if i&(1<<bit) != 0 {
				m[id] = "hi"
			} else {
				m[id] = "lo"
			}
		}
		return m
	}
	for i := 0; i < 8; i++ {
		p := rat(1, 64)
		if i == 0 {
			p = rat(57, 64)
		}
		inst.Candidates = append(inst.Candidates, Candidate{ID: string(rune('A' + i)), Prob: p})
	}
	inst.Tests = []Test{
		{Name: "bit0", Cost: 1, Outcomes: outcomes(0)},
		{Name: "bit1", Cost: 1, Outcomes: outcomes(1)},
		{Name: "bit2", Cost: 1, Outcomes: outcomes(2)},
		{Name: "isA", Cost: 2, Outcomes: map[string]string{
			"A": "yes", "B": "no", "C": "no", "D": "no",
			"E": "no", "F": "no", "G": "no", "H": "no",
		}},
	}
	root, err := Solve(inst)
	if err != nil {
		t.Fatal(err)
	}
	// isA first: A costs 2, everyone else costs 4 -> worst 4, same as
	// three binary tests (worst 3)? No: binary-only worst is 3 < 4,
	// so the optimum must start with a binary test.
	if root.Worst != 3 {
		t.Fatalf("worst = %d, want 3", root.Worst)
	}
	if err := ExhaustiveVerify(inst, root); err != nil {
		t.Fatal(err)
	}
}

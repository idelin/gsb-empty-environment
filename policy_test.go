package policy

import (
	"fmt"
	"math/big"
	"math/rand"
	"testing"
)

func rat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("bad rational: " + s)
	}
	return r
}

func uniformProbs(n int) []*big.Rat {
	ps := make([]*big.Rat, n)
	for i := range ps {
		ps[i] = rat(fmt.Sprintf("1/%d", n))
	}
	return ps
}

// Two worlds, two mirror-image actions, and a probe that reveals the world.
// Probing must beat acting blindly.
func TestSolveProbeBeatsBlind(t *testing.T) {
	p := &Problem{
		NumWorlds:  2,
		NumActions: 2,
		Loss:       [][]int{{0, 10}, {10, 0}},
		ActObs:     [][]string{{"x", "x"}, {"y", "y"}},
		Probes: []Probe{
			{Name: "peek", Cost: 1, Obs: []string{"w0", "w1"}},
		},
		MaxProbes: 1,
		Prob:      uniformProbs(2),
	}
	sol, err := p.Solve()
	if err != nil {
		t.Fatal(err)
	}
	if sol.Worst != 1 {
		t.Fatalf("Worst = %d, want 1 (probe cost only)", sol.Worst)
	}
	if sol.Expected.Cmp(rat("1")) != 0 {
		t.Fatalf("Expected = %s, want 1", sol.Expected.RatString())
	}
	if err := p.CheckPolicy(sol.Tree); err != nil {
		t.Fatalf("CheckPolicy: %v", err)
	}
	// World 0 must take A0, world 1 must take A1.
	rs, err := p.ReplayAll(sol.Tree)
	if err != nil {
		t.Fatal(err)
	}
	if rs[0].Action != 0 || rs[1].Action != 1 {
		t.Fatalf("actions = A%d, A%d; want A0, A1", rs[0].Action, rs[1].Action)
	}
	if rs[0].Total != 1 || rs[1].Total != 1 {
		t.Fatalf("totals = %d, %d; want 1, 1", rs[0].Total, rs[1].Total)
	}
	// Blind action would have worst case 10.
	blind := &Problem{
		NumWorlds: 2, NumActions: 2,
		Loss: p.Loss, ActObs: p.ActObs,
		MaxProbes: 0, Prob: uniformProbs(2),
	}
	bsol, err := blind.Solve()
	if err != nil {
		t.Fatal(err)
	}
	if bsol.Worst != 10 {
		t.Fatalf("blind Worst = %d, want 10", bsol.Worst)
	}
}

// When the worst case ties, the rational expectation must decide.
func TestExpectationTieBreak(t *testing.T) {
	p := &Problem{
		NumWorlds:  2,
		NumActions: 2,
		Loss:       [][]int{{5, 5}, {5, 0}},
		ActObs:     [][]string{{"a", "a"}, {"b", "b"}},
		MaxProbes:  0,
		Prob:       []*big.Rat{rat("1/3"), rat("2/3")},
	}
	sol, err := p.Solve()
	if err != nil {
		t.Fatal(err)
	}
	if sol.Worst != 5 {
		t.Fatalf("Worst = %d, want 5", sol.Worst)
	}
	// A0: 5; A1: 5/3. A1 must win on expectation.
	if !sol.Tree.IsLeaf() || sol.Tree.Action != 1 {
		t.Fatalf("tree = %v, want leaf A1", sol.Tree)
	}
	if sol.Expected.Cmp(rat("5/3")) != 0 {
		t.Fatalf("Expected = %s, want 5/3", sol.Expected.RatString())
	}
}

// A clairvoyant policy (worlds with identical observation histories but
// different actions) is unrepresentable as a tree; a tree that simply
// forgets a branch must be rejected.
func TestCheckPolicyRejectsBrokenTrees(t *testing.T) {
	p := &Problem{
		NumWorlds:  2,
		NumActions: 2,
		Loss:       [][]int{{0, 10}, {10, 0}},
		ActObs:     [][]string{{"x", "x"}, {"y", "y"}},
		Probes:     []Probe{{Cost: 1, Obs: []string{"w0", "w1"}}},
		MaxProbes:  1,
		Prob:       uniformProbs(2),
	}
	missing := &Node{Probe: 0, Action: -1, Children: map[string]*Node{"w0": Leaf(0)}}
	if err := p.CheckPolicy(missing); err == nil {
		t.Fatal("CheckPolicy accepted a tree with a missing branch")
	}
	overBudget := &Node{Probe: 0, Action: -1, Children: map[string]*Node{
		"w0": {Probe: 0, Action: -1, Children: map[string]*Node{"w0": Leaf(0), "w1": Leaf(1)}},
		"w1": Leaf(1),
	}}
	if err := p.CheckPolicy(overBudget); err == nil {
		t.Fatal("CheckPolicy accepted a tree exceeding the probe budget / reusing a probe")
	}
}

// randomProblem builds a small random instance for cross-checking.
func randomProblem(rng *rand.Rand) *Problem {
	n := 1 + rng.Intn(5)
	m := 1 + rng.Intn(3)
	np := rng.Intn(4)
	p := &Problem{
		NumWorlds:  n,
		NumActions: m,
		Loss:       make([][]int, m),
		ActObs:     make([][]string, m),
		MaxProbes:  rng.Intn(3), // 0..2
	}
	symbols := []string{"a", "b", "c"}
	for a := 0; a < m; a++ {
		p.Loss[a] = make([]int, n)
		p.ActObs[a] = make([]string, n)
		for w := 0; w < n; w++ {
			p.Loss[a][w] = rng.Intn(16) - 5
			p.ActObs[a][w] = symbols[rng.Intn(2)]
		}
	}
	for i := 0; i < np; i++ {
		obs := make([]string, n)
		for w := range obs {
			obs[w] = symbols[rng.Intn(len(symbols))]
		}
		p.Probes = append(p.Probes, Probe{Cost: rng.Intn(5), Obs: obs})
	}
	// Random positive rational probabilities, normalized.
	weights := make([]int64, n)
	var total int64
	for w := range weights {
		weights[w] = int64(1 + rng.Intn(9))
		total += weights[w]
	}
	p.Prob = make([]*big.Rat, n)
	for w := range p.Prob {
		p.Prob[w] = big.NewRat(weights[w], total)
	}
	return p
}

// The DP solver must match full policy enumeration on small instances,
// both in the final objective and in the whole Pareto frontier.
func TestSolveMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 300; trial++ {
		p := randomProblem(rng)
		sol, err := p.Solve()
		if err != nil {
			t.Fatalf("trial %d: Solve: %v", trial, err)
		}
		best, _, err := p.BruteForce()
		if err != nil {
			t.Fatalf("trial %d: BruteForce: %v", trial, err)
		}
		if sol.Worst != best.Worst || sol.Expected.Cmp(best.Expected) != 0 {
			t.Fatalf("trial %d: Solve=(%d,%s) BruteForce=(%d,%s)",
				trial, sol.Worst, sol.Expected.RatString(), best.Worst, best.Expected.RatString())
		}
		// The DP frontier at the root must equal the Pareto-pruned
		// enumeration set.
		s := &solver{p: p, memo: make(map[stateKey]*stateSol), probSums: make(map[uint32]*big.Rat)}
		full := uint32(1<<uint(p.NumWorlds)) - 1
		front := s.solve(full, p.MaxProbes, 0).front
		b := &brute{p: p, memo: make(map[stateKey]*bruteSol)}
		bset := b.solve(full, p.MaxProbes, 0).pairs
		bch := make([]choice, len(bset))
		bfront, _ := pruneFront(bset, bch)
		if len(front) != len(bfront) {
			t.Fatalf("trial %d: frontier sizes %d vs %d", trial, len(front), len(bfront))
		}
		for i := range front {
			if front[i].worst != bfront[i].worst || front[i].exp.Cmp(bfront[i].exp) != 0 {
				t.Fatalf("trial %d: frontier[%d] = (%d,%s), brute (%d,%s)",
					trial, i, front[i].worst, front[i].exp.RatString(),
					bfront[i].worst, bfront[i].exp.RatString())
			}
		}
	}
}

// Replays must be consistent with the reported objective, and the policy
// must be non-clairvoyant.
func TestReplayConsistency(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for trial := 0; trial < 300; trial++ {
		p := randomProblem(rng)
		sol, err := p.Solve()
		if err != nil {
			t.Fatalf("trial %d: Solve: %v", trial, err)
		}
		if err := p.CheckPolicy(sol.Tree); err != nil {
			t.Fatalf("trial %d: CheckPolicy: %v", trial, err)
		}
		rs, err := p.ReplayAll(sol.Tree)
		if err != nil {
			t.Fatalf("trial %d: ReplayAll: %v", trial, err)
		}
		worst := 0
		exp := new(big.Rat)
		for w, r := range rs {
			if r.Total != r.ProbeCost+r.ActionLoss {
				t.Fatalf("trial %d: world %d total mismatch", trial, w)
			}
			if len(r.Events) > p.MaxProbes {
				t.Fatalf("trial %d: world %d used %d probes > budget", trial, w, len(r.Events))
			}
			if w == 0 || r.Total > worst {
				worst = r.Total
			}
			exp.Add(exp, new(big.Rat).Mul(p.Prob[w], new(big.Rat).SetInt64(int64(r.Total))))
		}
		if worst != sol.Worst {
			t.Fatalf("trial %d: replay worst %d != objective %d", trial, worst, sol.Worst)
		}
		if exp.Cmp(sol.Expected) != 0 {
			t.Fatalf("trial %d: replay expected %s != objective %s",
				trial, exp.RatString(), sol.Expected.RatString())
		}
		// Non-clairvoyance: identical observation histories must give
		// identical actions.
		seen := make(map[string]int)
		for _, r := range rs {
			h := historyKey(r.Events)
			if a, ok := seen[h]; ok && a != r.Action {
				t.Fatalf("trial %d: clairvoyant tree at history %q", trial, h)
			}
			seen[h] = r.Action
		}
	}
}

// Solving twice must give identical trees (deterministic tie-breaking).
func TestDeterministic(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for trial := 0; trial < 50; trial++ {
		p := randomProblem(rng)
		s1, err := p.Solve()
		if err != nil {
			t.Fatal(err)
		}
		s2, err := p.Solve()
		if err != nil {
			t.Fatal(err)
		}
		if p.TreeString(s1.Tree) != p.TreeString(s2.Tree) {
			t.Fatalf("trial %d: nondeterministic tree", trial)
		}
	}
}

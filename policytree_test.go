package policytree

import (
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

// handExample: probing perfectly distinguishes the worlds. Acting blindly
// costs worst 10 either way; probing first costs 1 + max(0, 0) = 1 because
// each branch then picks the action that is free in that world.
func handExample() *Problem {
	return &Problem{
		Worlds:  []string{"healthy", "sick"},
		Weights: []*big.Rat{rat("1/2"), rat("1/2")},
		Actions: []Action{
			{Name: "wait", Losses: []int64{0, 10}, Symbols: []string{"fine", "bad"}},
			{Name: "treat", Losses: []int64{10, 0}, Symbols: []string{"mild", "cured"}},
		},
		Probes: []Probe{
			{Name: "test", Cost: 1, Outcomes: []string{"neg", "pos"}},
		},
		MaxProbes: 2,
	}
}

func TestSolveHandExample(t *testing.T) {
	p := handExample()
	cost, tree, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if cost.Worst != 1 {
		t.Errorf("worst = %d, want 1", cost.Worst)
	}
	if want := rat("1"); cost.Exp.Cmp(want) != 0 {
		t.Errorf("exp = %s, want %s", cost.Exp, want)
	}
	if tree.Kind != ProbeNode {
		t.Fatalf("root should be a probe node, got kind %d", tree.Kind)
	}
	if got := tree.Children["neg"].Action; p.Actions[got].Name != "wait" {
		t.Errorf("after neg: action %s, want wait", p.Actions[got].Name)
	}
	if got := tree.Children["pos"].Action; p.Actions[got].Name != "treat" {
		t.Errorf("after pos: action %s, want treat", p.Actions[got].Name)
	}
}

// TestTieBreakByExpectation: both actions have worst-case 3; a1 has the
// lower expected loss (1/3*3 + 2/3*0 = 1 vs 3) and must win.
func TestTieBreakByExpectation(t *testing.T) {
	p := &Problem{
		Worlds:  []string{"w0", "w1"},
		Weights: []*big.Rat{rat("1/3"), rat("2/3")},
		Actions: []Action{
			{Name: "a0", Losses: []int64{3, 3}, Symbols: []string{"x", "x"}},
			{Name: "a1", Losses: []int64{3, 0}, Symbols: []string{"x", "y"}},
		},
		MaxProbes: 0,
	}
	cost, tree, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if tree.Kind != ActionNode || p.Actions[tree.Action].Name != "a1" {
		t.Fatalf("expected action a1, got tree:\n%s", tree.String(p))
	}
	if cost.Worst != 3 || cost.Exp.Cmp(rat("1")) != 0 {
		t.Errorf("cost = %s, want worst=3 exp=1", cost)
	}
}

// TestExactRationalExpectation checks the expectation is an exact rational.
func TestExactRationalExpectation(t *testing.T) {
	p := &Problem{
		Worlds:  []string{"w0", "w1"},
		Weights: []*big.Rat{rat("1/3"), rat("2/3")},
		Actions: []Action{
			{Name: "a0", Losses: []int64{0, 1}, Symbols: []string{"x", "y"}},
		},
		MaxProbes: 0,
	}
	cost, _, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if want := rat("2/3"); cost.Exp.Cmp(want) != 0 {
		t.Errorf("exp = %s, want %s", cost.Exp, want)
	}
}

// randomProblem builds a small random problem suitable for exhaustive
// enumeration: at most 3 worlds and binary probe outcomes keep the number of
// policy trees manageable.
func randomProblem(rng *rand.Rand) *Problem {
	n := 2 + rng.Intn(2) // 2..3 worlds
	na := 2 + rng.Intn(2)
	np := rng.Intn(3) // 0..2 probes
	p := &Problem{MaxProbes: 2}
	for w := 0; w < n; w++ {
		p.Worlds = append(p.Worlds, string(rune('a'+w)))
		p.Weights = append(p.Weights, big.NewRat(int64(1+rng.Intn(5)), 1))
	}
	for a := 0; a < na; a++ {
		action := Action{Name: string(rune('A' + a))}
		for w := 0; w < n; w++ {
			action.Losses = append(action.Losses, int64(rng.Intn(10)))
			action.Symbols = append(action.Symbols, string(rune('x'+rng.Intn(2))))
		}
		p.Actions = append(p.Actions, action)
	}
	for pr := 0; pr < np; pr++ {
		probe := Probe{Name: string(rune('p' + pr)), Cost: int64(rng.Intn(4))}
		for w := 0; w < n; w++ {
			probe.Outcomes = append(probe.Outcomes, string(rune('0'+rng.Intn(2))))
		}
		p.Probes = append(p.Probes, probe)
	}
	return p
}

// TestBruteForceCrossCheck compares the dynamic program against explicit
// enumeration of every policy tree on many small random problems.
func TestBruteForceCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 60; i++ {
		p := randomProblem(rng)
		cost, tree, err := Solve(p)
		if err != nil {
			t.Fatal(err)
		}
		best, err := BruteForce(p)
		if err != nil {
			t.Fatal(err)
		}
		if cost.Worst != best.Worst || cost.Exp.Cmp(best.Exp) != 0 {
			t.Fatalf("instance %d: solver=%s brute=%s\ntree:\n%s", i, cost, best, tree.String(p))
		}
	}
}

// TestNonAnticipationAndReplay verifies the solved policy is a function of
// the observation history and that per-world replays reproduce the reported
// worst-case and expected costs exactly.
func TestNonAnticipationAndReplay(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 60; i++ {
		p := randomProblem(rng)
		cost, tree, err := Solve(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := VerifyNonAnticipation(p, tree); err != nil {
			t.Fatalf("instance %d: %v", i, err)
		}
		entries, err := Replay(p, tree)
		if err != nil {
			t.Fatal(err)
		}
		var worst int64
		exp := new(big.Rat)
		total := p.TotalWeight()
		for w, e := range entries {
			if e.Total != e.ProbeCost+e.Loss {
				t.Fatalf("instance %d world %d: inconsistent total", i, w)
			}
			if len(e.Steps) > p.MaxProbes {
				t.Fatalf("instance %d world %d: probe budget exceeded", i, w)
			}
			if e.Total > worst {
				worst = e.Total
			}
			share := new(big.Rat).Quo(p.Weights[w], total)
			exp.Add(exp, share.Mul(share, new(big.Rat).SetInt64(e.Total)))
		}
		if worst != cost.Worst {
			t.Fatalf("instance %d: replay worst %d != solver worst %d", i, worst, cost.Worst)
		}
		if exp.Cmp(cost.Exp) != 0 {
			t.Fatalf("instance %d: replay exp %s != solver exp %s", i, exp, cost.Exp)
		}
	}
}

// TestDetectsAnticipatingPolicy feeds the verifier a hand-built "policy"
// that cannot be represented as a function of observations: two worlds share
// every observation but demand different actions. We emulate it via a tree
// whose probe outcomes are identical across worlds yet whose children differ
// per world — impossible to construct as a tree — so instead we check the
// verifier flags an invalid tree (missing branch) and accepts valid ones.
func TestVerifierRejectsBrokenTree(t *testing.T) {
	p := handExample()
	broken := &Node{
		Kind:     ProbeNode,
		Probe:    0,
		Outcomes: []string{"neg"},
		Children: map[string]*Node{"neg": {Kind: ActionNode, Action: 0}},
	}
	if err := VerifyNonAnticipation(p, broken); err == nil {
		t.Fatal("expected error for tree missing the pos branch")
	}
}

func TestValidation(t *testing.T) {
	p := handExample()
	p.MaxProbes = 3
	if _, _, err := Solve(p); err == nil {
		t.Fatal("expected error: MaxProbes > 2")
	}
	p = handExample()
	p.Actions[0].Losses = []int64{1}
	if _, _, err := Solve(p); err == nil {
		t.Fatal("expected error: loss vector length mismatch")
	}
}

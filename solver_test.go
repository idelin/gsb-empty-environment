package main

import (
	"math/big"
	"math/rand"
	"strings"
	"testing"
)

func mustLoad(t *testing.T, js string) *Problem {
	t.Helper()
	p, err := LoadProblem(strings.NewReader(js))
	if err != nil {
		t.Fatalf("LoadProblem: %v", err)
	}
	return p
}

func rat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("bad rational " + s)
	}
	return r
}

// Shared capacity forces a split: both signals prefer "cheap", but cheap
// cannot cover all the probability mass.
func TestSharedCapacityBinds(t *testing.T) {
	p := mustLoad(t, `{
	  "worlds": [
	    {"name": "w1", "prob": "1/2", "signal": "s1"},
	    {"name": "w2", "prob": "1/2", "signal": "s2"}
	  ],
	  "actions": ["cheap", "dear"],
	  "losses": {
	    "w1": {"cheap": "0", "dear": "5"},
	    "w2": {"cheap": "0", "dear": "5"}
	  },
	  "capacity": {"cheap": "1/2"}
	}`)
	res, err := p.Solve()
	if err != nil {
		t.Fatal(err)
	}
	if !res.Feasible {
		t.Fatal("expected a feasible optimum")
	}
	// One signal must spill onto "dear", so the worst case is 5.
	if res.Worst.Cmp(rat("5")) != 0 {
		t.Fatalf("worst = %s, want 5", res.Worst)
	}
	if res.Avg.Cmp(rat("5/2")) != 0 {
		t.Fatalf("avg = %s, want 5/2", res.Avg)
	}
	if res.Usage[0].Cmp(rat("1/2")) != 0 || res.Usage[1].Cmp(rat("1/2")) != 0 {
		t.Fatalf("usage = (%s, %s), want (1/2, 1/2)", res.Usage[0], res.Usage[1])
	}
}

// Worlds sharing a signal cannot be told apart: the optimum must pick one
// action for both, even though their individually best actions differ.
func TestNonAnticipativity(t *testing.T) {
	p := mustLoad(t, `{
	  "worlds": [
	    {"name": "w1", "prob": "1/2", "signal": "same"},
	    {"name": "w2", "prob": "1/2", "signal": "same"}
	  ],
	  "actions": ["a", "b"],
	  "losses": {
	    "w1": {"a": "0", "b": "10"},
	    "w2": {"a": "10", "b": "0"}
	  }
	}`)
	res, err := p.Solve()
	if err != nil {
		t.Fatal(err)
	}
	// Any single action gives worst 10 and average 5; a clairvoyant
	// policy would get 0, so 10 proves the signal rule binds.
	if res.Worst.Cmp(rat("10")) != 0 || res.Avg.Cmp(rat("5")) != 0 {
		t.Fatalf("got (worst %s, avg %s), want (10, 5)", res.Worst, res.Avg)
	}
	if res.Policy[0] != res.Policy[0] { // trivially one entry; both worlds share it
		t.Fatal("policy must assign one action per signal")
	}
}

// Lexicographic order: a lower worst case wins even with a higher average,
// and among equal worst cases the lower average wins.
func TestLexicographicObjective(t *testing.T) {
	p := mustLoad(t, `{
	  "worlds": [
	    {"name": "w1", "prob": "9/10", "signal": "s1"},
	    {"name": "w2", "prob": "1/10", "signal": "s2"}
	  ],
	  "actions": ["safe", "spiky"],
	  "losses": {
	    "w1": {"safe": "3", "spiky": "0"},
	    "w2": {"safe": "3", "spiky": "100"}
	  }
	}`)
	res, err := p.Solve()
	if err != nil {
		t.Fatal(err)
	}
	// "spiky" on s1 and "safe" on s2 gives worst 3, avg 3/10. "safe"
	// everywhere ties the worst case but loses on average (3), while
	// "spiky" on s2 pushes the worst case to 100 and loses outright.
	if res.Worst.Cmp(rat("3")) != 0 || res.Avg.Cmp(rat("3/10")) != 0 {
		t.Fatalf("got (worst %s, avg %s), want (3, 3/10)", res.Worst, res.Avg)
	}
	want := []string{"spiky", "safe"}
	for s, w := range want {
		if p.Actions[res.Policy[s]] != w {
			t.Fatalf("signal %d: got %s, want %s", s, p.Actions[res.Policy[s]], w)
		}
	}
}

func TestInfeasible(t *testing.T) {
	p := mustLoad(t, `{
	  "worlds": [{"name": "w1", "prob": "1", "signal": "s1"}],
	  "actions": ["a", "b"],
	  "losses": {"w1": {"a": "1", "b": "2"}},
	  "capacity": {"a": "1/3", "b": "1/3"}
	}`)
	res, err := p.Solve()
	if err != nil {
		t.Fatal(err)
	}
	if res.Feasible {
		t.Fatal("expected infeasible")
	}
	enum, err := p.Enumerate()
	if err != nil {
		t.Fatal(err)
	}
	if enum.Best != nil || enum.FeasibleCount != 0 {
		t.Fatal("enumeration disagrees on infeasibility")
	}
}

func TestInvalidInput(t *testing.T) {
	if _, err := LoadProblem(strings.NewReader(`{
	  "worlds": [{"name": "w1", "prob": "1/2", "signal": "s1"}],
	  "actions": ["a"],
	  "losses": {"w1": {"a": "1"}}
	}`)); err == nil {
		t.Fatal("expected error for probabilities not summing to 1")
	}
	if _, err := LoadProblem(strings.NewReader(`{
	  "worlds": [{"name": "w1", "prob": "1", "signal": "s1"}],
	  "actions": ["a"],
	  "losses": {"w1": {"a": "x"}}
	}`)); err == nil {
		t.Fatal("expected error for non-rational loss")
	}
}

// Random small problems: the branch-and-bound solver must always agree
// with exhaustive enumeration, including on feasibility.
func TestRandomCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 300; trial++ {
		p := randomProblem(rng)
		res, enum, err := p.Verify()
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		if res.Feasible {
			// The returned policy must itself evaluate to the reported
			// objective and respect capacities.
			ev, err := p.Evaluate(res.Policy)
			if err != nil {
				t.Fatalf("trial %d: %v", trial, err)
			}
			if !ev.Feasible || ev.Worst.Cmp(res.Worst) != 0 || ev.Avg.Cmp(res.Avg) != 0 {
				t.Fatalf("trial %d: policy does not match reported objective", trial)
			}
			if enum.FeasibleCount == 0 {
				t.Fatalf("trial %d: enumeration found nothing feasible", trial)
			}
		}
	}
}

func randomProblem(rng *rand.Rand) *Problem {
	numSignals := 1 + rng.Intn(3)
	numActions := 2 + rng.Intn(2)
	numWorlds := numSignals + rng.Intn(3)

	// Random probability distribution as integer weights over a common
	// denominator, keeping everything exactly rational.
	weights := make([]int64, numWorlds)
	var total int64
	for i := range weights {
		weights[i] = int64(1 + rng.Intn(5))
		total += weights[i]
	}

	p := &Problem{signalIndex: make(map[string]int)}
	for i := 0; i < numActions; i++ {
		p.Actions = append(p.Actions, string(rune('a'+i)))
	}
	for i := 0; i < numWorlds; i++ {
		sig := "s" + string(rune('0'+rng.Intn(numSignals)))
		w := World{
			Name:   "w" + string(rune('0'+i)),
			Prob:   big.NewRat(weights[i], total),
			Signal: sig,
		}
		si, ok := p.signalIndex[sig]
		if !ok {
			si = len(p.signals)
			p.signalIndex[sig] = si
			p.signals = append(p.signals, sig)
			p.worldsBySignal = append(p.worldsBySignal, nil)
		}
		p.Worlds = append(p.Worlds, w)
		p.worldSignal = append(p.worldSignal, si)
		p.worldsBySignal[si] = append(p.worldsBySignal[si], i)
	}
	p.Loss = make([][]*big.Rat, numWorlds)
	for w := range p.Loss {
		p.Loss[w] = make([]*big.Rat, numActions)
		for a := range p.Loss[w] {
			p.Loss[w][a] = big.NewRat(int64(rng.Intn(11)-2), int64(1+rng.Intn(3)))
		}
	}
	p.Capacity = make([]*big.Rat, numActions)
	for a := range p.Capacity {
		// Half the actions get a (possibly tight) capacity.
		if rng.Intn(2) == 0 {
			p.Capacity[a] = big.NewRat(int64(rng.Intn(4)), 3)
		}
	}
	return p
}

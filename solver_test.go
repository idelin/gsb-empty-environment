package main

import (
	"math/big"
	"testing"
)

func rat(s string) *big.Rat {
	v, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("bad rational: " + s)
	}
	return v
}

func mat(name, size, cost string) Material {
	m := Material{Name: name}
	if size != "" {
		m.Purchasable = true
		m.PackageSize = rat(size)
		m.PackageCost = rat(cost)
	}
	return m
}

func rule(name string, in, out map[string]string) Rule {
	r := Rule{Name: name, Inputs: map[string]*big.Rat{}, Outputs: map[string]*big.Rat{}}
	for k, v := range in {
		r.Inputs[k] = rat(v)
	}
	for k, v := range out {
		r.Outputs[k] = rat(v)
	}
	return r
}

func demands(kv ...string) map[string]*big.Rat {
	d := map[string]*big.Rat{}
	for i := 0; i < len(kv); i += 2 {
		d[kv[i]] = rat(kv[i+1])
	}
	return d
}

func packagesOf(plan *Plan, name string) string {
	for _, p := range plan.Purchases {
		if p.Material == name {
			return p.Packages.RatString()
		}
	}
	return "0"
}

func usageOf(plan *Plan, name string) string {
	for _, u := range plan.RuleUsage {
		if u.Rule == name {
			return u.Amount.RatString()
		}
	}
	return "0"
}

func TestLPBasic(t *testing.T) {
	// min 3x + 2y, x+y >= 4, x+3y >= 6  ->  optimum 8 at (0,4)
	cons := []lpConstraint{
		{coeffs: map[int]*big.Rat{0: rat("1"), 1: rat("1")}, sense: senseGeq, rhs: rat("4")},
		{coeffs: map[int]*big.Rat{0: rat("1"), 1: rat("3")}, sense: senseGeq, rhs: rat("6")},
	}
	st, obj, x := solveLP(2, cons, []*big.Rat{rat("3"), rat("2")})
	if st != lpOptimal {
		t.Fatalf("status %v", st)
	}
	if obj.Cmp(rat("8")) != 0 {
		t.Fatalf("obj = %v, want 8", obj)
	}
	if x[0].Cmp(rat("0")) != 0 || x[1].Cmp(rat("4")) != 0 {
		t.Fatalf("x = (%v, %v), want (0, 4)", x[0], x[1])
	}
}

func TestLPInfeasible(t *testing.T) {
	cons := []lpConstraint{
		{coeffs: map[int]*big.Rat{0: rat("1")}, sense: senseGeq, rhs: rat("2")},
		{coeffs: map[int]*big.Rat{0: rat("1")}, sense: senseLeq, rhs: rat("1")},
	}
	st, _, _ := solveLP(1, cons, []*big.Rat{rat("1")})
	if st != lpInfeasible {
		t.Fatalf("status %v, want infeasible", st)
	}
}

func TestDirectPurchaseRoundsUpPackages(t *testing.T) {
	p := &Problem{
		Materials: []Material{mat("A", "10", "7")},
		Demands:   demands("A", "25"),
	}
	plan, cycle, feasible, err := SolveProblem(p)
	if err != nil || cycle != nil || !feasible {
		t.Fatalf("err=%v cycle=%v feasible=%v", err, cycle, feasible)
	}
	if plan.TotalCost.Cmp(rat("21")) != 0 {
		t.Fatalf("cost = %v, want 21", plan.TotalCost)
	}
	if got := packagesOf(plan, "A"); got != "3" {
		t.Fatalf("packages A = %s, want 3", got)
	}
}

func TestSubstitutionBeatsDirectPurchase(t *testing.T) {
	p := &Problem{
		Materials: []Material{mat("A", "10", "10"), mat("B", "5", "6")},
		Demands:   demands("B", "25"),
		Rules:     []Rule{rule("conv", map[string]string{"A": "1"}, map[string]string{"B": "3"})},
	}
	plan, _, feasible, err := SolveProblem(p)
	if err != nil || !feasible {
		t.Fatalf("err=%v feasible=%v", err, feasible)
	}
	// 1 package of A (cost 10) yields 30 B >= 25; direct B would cost 30.
	if plan.TotalCost.Cmp(rat("10")) != 0 {
		t.Fatalf("cost = %v, want 10", plan.TotalCost)
	}
	if got := usageOf(plan, "conv"); got != "25/3" {
		t.Fatalf("conv usage = %s, want 25/3", got)
	}
}

func TestMultiLevelSubstitution(t *testing.T) {
	p := &Problem{
		Materials: []Material{mat("A", "5", "3"), mat("B", "", ""), mat("C", "", "")},
		Demands:   demands("C", "8"),
		Rules: []Rule{
			rule("a2b", map[string]string{"A": "1"}, map[string]string{"B": "2"}),
			rule("b2c", map[string]string{"B": "1"}, map[string]string{"C": "2"}),
		},
	}
	plan, _, feasible, err := SolveProblem(p)
	if err != nil || !feasible {
		t.Fatalf("err=%v feasible=%v", err, feasible)
	}
	if plan.TotalCost.Cmp(rat("3")) != 0 {
		t.Fatalf("cost = %v, want 3", plan.TotalCost)
	}
	if got := usageOf(plan, "a2b"); got != "2" {
		t.Fatalf("a2b usage = %s, want 2", got)
	}
	if got := usageOf(plan, "b2c"); got != "4" {
		t.Fatalf("b2c usage = %s, want 4", got)
	}
}

func TestAuxiliaryMaterialConsumption(t *testing.T) {
	p := &Problem{
		Materials: []Material{mat("A", "10", "5"), mat("B", "", ""), mat("C", "1", "1")},
		Demands:   demands("B", "4"),
		Rules: []Rule{
			rule("conv", map[string]string{"A": "1", "C": "1"}, map[string]string{"B": "2"}),
		},
	}
	plan, _, feasible, err := SolveProblem(p)
	if err != nil || !feasible {
		t.Fatalf("err=%v feasible=%v", err, feasible)
	}
	// conv x2: needs 2 A (1 package, 5) and 2 C (2 packages, 2) -> 7.
	if plan.TotalCost.Cmp(rat("7")) != 0 {
		t.Fatalf("cost = %v, want 7", plan.TotalCost)
	}
	if got := packagesOf(plan, "C"); got != "2" {
		t.Fatalf("packages C = %s, want 2", got)
	}
}

func TestProductiveCycleRejected(t *testing.T) {
	p := &Problem{
		Materials: []Material{mat("A", "1", "1"), mat("B", "1", "1")},
		Demands:   demands("B", "2"),
		Rules: []Rule{
			rule("r1", map[string]string{"A": "1"}, map[string]string{"B": "2"}),
			rule("r2", map[string]string{"B": "1"}, map[string]string{"A": "1"}),
		},
	}
	_, cycle, _, err := SolveProblem(p)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if len(cycle) != 2 {
		t.Fatalf("cycle = %v, want [r1 r2]", cycle)
	}
}

func TestSelfAmplifyingRuleRejected(t *testing.T) {
	p := &Problem{
		Materials: []Material{mat("A", "1", "1")},
		Demands:   demands("A", "1"),
		Rules:     []Rule{rule("dup", map[string]string{"A": "1"}, map[string]string{"A": "2"})},
	}
	_, cycle, _, err := SolveProblem(p)
	if err != nil || len(cycle) != 1 {
		t.Fatalf("err=%v cycle=%v, want [dup]", err, cycle)
	}
}

func TestRationalRatioCycleRejected(t *testing.T) {
	// 2A -> 3B and 3B -> 4A: jointly +2A per round.
	p := &Problem{
		Materials: []Material{mat("A", "1", "1"), mat("B", "1", "1")},
		Demands:   demands("A", "1"),
		Rules: []Rule{
			rule("r1", map[string]string{"A": "2"}, map[string]string{"B": "3"}),
			rule("r2", map[string]string{"B": "3"}, map[string]string{"A": "4"}),
		},
	}
	_, cycle, _, err := SolveProblem(p)
	if err != nil || len(cycle) != 2 {
		t.Fatalf("err=%v cycle=%v, want [r1 r2]", err, cycle)
	}
}

func TestBreakEvenCycleAllowed(t *testing.T) {
	// 1A -> 1B and 1B -> 1A conserve quantity: must NOT be rejected.
	p := &Problem{
		Materials: []Material{mat("A", "1", "1"), mat("B", "", "")},
		Demands:   demands("B", "2"),
		Rules: []Rule{
			rule("f", map[string]string{"A": "1"}, map[string]string{"B": "1"}),
			rule("b", map[string]string{"B": "1"}, map[string]string{"A": "1"}),
		},
	}
	plan, cycle, feasible, err := SolveProblem(p)
	if err != nil || cycle != nil || !feasible {
		t.Fatalf("err=%v cycle=%v feasible=%v", err, cycle, feasible)
	}
	if plan.TotalCost.Cmp(rat("2")) != 0 {
		t.Fatalf("cost = %v, want 2", plan.TotalCost)
	}
}

func TestInfeasible(t *testing.T) {
	p := &Problem{
		Materials: []Material{mat("A", "1", "1"), mat("B", "", "")},
		Demands:   demands("B", "1"),
	}
	_, _, feasible, err := SolveProblem(p)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if feasible {
		t.Fatalf("expected infeasible")
	}
}

func TestIntegerPackagesForceMix(t *testing.T) {
	// Demand 7 of B. conv turns 1 A -> 2 B. A comes in packs of 4 (cost 4),
	// B comes in packs of 3 (cost 5).
	// Best: 1 pack A (4 A -> 8 B) cost 4. Check it does not buy 2 packs of B (10).
	p := &Problem{
		Materials: []Material{mat("A", "4", "4"), mat("B", "3", "5")},
		Demands:   demands("B", "7"),
		Rules:     []Rule{rule("conv", map[string]string{"A": "1"}, map[string]string{"B": "2"})},
	}
	plan, _, feasible, err := SolveProblem(p)
	if err != nil || !feasible {
		t.Fatalf("err=%v feasible=%v", err, feasible)
	}
	if plan.TotalCost.Cmp(rat("4")) != 0 {
		t.Fatalf("cost = %v, want 4", plan.TotalCost)
	}
	if got := packagesOf(plan, "A"); got != "1" {
		t.Fatalf("packages A = %s, want 1", got)
	}
}

func TestSurplusReported(t *testing.T) {
	p := &Problem{
		Materials: []Material{mat("A", "10", "7")},
		Demands:   demands("A", "25"),
	}
	plan, _, feasible, err := SolveProblem(p)
	if err != nil || !feasible {
		t.Fatalf("err=%v feasible=%v", err, feasible)
	}
	for _, mp := range plan.Materials {
		if mp.Name == "A" && mp.Surplus.Cmp(rat("5")) != 0 {
			t.Fatalf("surplus = %v, want 5", mp.Surplus)
		}
	}
}

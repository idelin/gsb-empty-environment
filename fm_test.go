package main

import (
	"math/big"
	"math/rand"
	"testing"
)

func rat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("bad rat " + s)
	}
	return r
}

func ineqFrom(coeffs []string, rhs string, strict bool, id string) *Ineq {
	cs := make([]*big.Rat, len(coeffs))
	for i, c := range coeffs {
		cs[i] = rat(c)
	}
	return newIneq(cs, rat(rhs), strict, id)
}

func provIDs(in *Ineq) map[string]bool {
	out := map[string]bool{}
	for _, d := range in.Prov {
		for _, t := range d {
			out[t.ID] = true
		}
	}
	return out
}

func TestBasicElimination(t *testing.T) {
	// c1: x + y <= 4 ; c2: x - y <= 2 ; c3: -x + y <= 1 ; eliminate x.
	ineqs := []*Ineq{
		ineqFrom([]string{"1", "1"}, "4", false, "c1"),
		ineqFrom([]string{"1", "-1"}, "2", false, "c2"),
		ineqFrom([]string{"-1", "1"}, "1", false, "c3"),
	}
	res, err := eliminateAll(ineqs, []int{0}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if res.Contradiction != nil {
		t.Fatal("unexpected contradiction")
	}
	if len(res.Kept) != 1 {
		t.Fatalf("expected 1 kept constraint, got %d", len(res.Kept))
	}
	k := res.Kept[0]
	// 2y <= 5 normalized: coeffs (0,2), rhs 5.
	if k.Coeffs[0].Sign() != 0 || k.Coeffs[1].Cmp(rat("2")) != 0 || k.RHS.Cmp(rat("5")) != 0 {
		t.Fatalf("unexpected kept constraint %s", k)
	}
	if k.Strict {
		t.Fatal("kept constraint should be non-strict")
	}
	ids := provIDs(k)
	if !ids["c1"] || !ids["c3"] {
		t.Fatalf("provenance should mention c1 and c3, got %v", ids)
	}
	// c3+c2 yields 0 <= 3, a tautology that must be recorded as removed.
	foundTaut := false
	for _, r := range res.Removed {
		if r.Ineq.isZero() {
			foundTaut = true
			ids := provIDs(r.Ineq)
			if !ids["c2"] || !ids["c3"] {
				t.Fatalf("tautology provenance wrong: %v", ids)
			}
		}
	}
	if !foundTaut {
		t.Fatal("expected a recorded tautology from c3+c2")
	}
}

func TestStrictPropagation(t *testing.T) {
	// c1: x - y < 0 ; c2: y <= 3 ; eliminate y -> x < 3 (strict).
	ineqs := []*Ineq{
		ineqFrom([]string{"1", "-1"}, "0", true, "c1"),
		ineqFrom([]string{"0", "1"}, "3", false, "c2"),
	}
	res, err := eliminateAll(ineqs, []int{1}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Kept) != 1 {
		t.Fatalf("expected 1 kept, got %d", len(res.Kept))
	}
	k := res.Kept[0]
	if !k.Strict {
		t.Fatal("strictness was not propagated")
	}
	if k.Coeffs[0].Cmp(rat("1")) != 0 || k.RHS.Cmp(rat("3")) != 0 {
		t.Fatalf("unexpected constraint %s", k)
	}
}

func TestOppositeInequalitiesCollapse(t *testing.T) {
	// y <= x and y >= x force y = x; eliminating y leaves only x <= 2.
	ineqs := []*Ineq{
		ineqFrom([]string{"-1", "1"}, "0", false, "c1"),
		ineqFrom([]string{"1", "-1"}, "0", false, "c2"),
		ineqFrom([]string{"1", "0"}, "2", false, "c3"),
	}
	res, err := eliminateAll(ineqs, []int{1}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if res.Contradiction != nil {
		t.Fatal("unexpected contradiction")
	}
	if len(res.Kept) != 1 || res.Kept[0].RHS.Cmp(rat("2")) != 0 {
		t.Fatalf("expected only x <= 2, got %v", res.Kept)
	}
}

func TestOppositeStrictContradiction(t *testing.T) {
	// y <= x and y > x are contradictory; eliminating y derives 0 < 0.
	ineqs := []*Ineq{
		ineqFrom([]string{"-1", "1"}, "0", false, "c1"),
		ineqFrom([]string{"1", "-1"}, "0", true, "c2"),
	}
	res, err := eliminateAll(ineqs, []int{1}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if res.Contradiction == nil {
		t.Fatal("expected contradiction")
	}
	if !res.Contradiction.Strict {
		t.Fatal("contradiction should be strict (0 < 0)")
	}
	ids := provIDs(res.Contradiction)
	if !ids["c1"] || !ids["c2"] {
		t.Fatalf("contradiction provenance wrong: %v", ids)
	}
}

func TestBudgetExceeded(t *testing.T) {
	var ineqs []*Ineq
	// 3 lower and 3 upper bounds on z -> 9 combinations.
	for i := 0; i < 3; i++ {
		ineqs = append(ineqs, ineqFrom([]string{"-1"}, big.NewInt(int64(i)).String(), false, "l"+string(rune('1'+i))))
		ineqs = append(ineqs, ineqFrom([]string{"1"}, big.NewInt(int64(3+i)).String(), false, "u"+string(rune('1'+i))))
	}
	_, err := eliminateAll(ineqs, []int{0}, 5)
	be, ok := err.(*BudgetExceededError)
	if !ok {
		t.Fatalf("expected BudgetExceededError, got %v", err)
	}
	if be.Budget != 5 {
		t.Fatalf("wrong budget in error: %v", be)
	}
}

func TestBudgetExceededReportHasNoConstraints(t *testing.T) {
	spec := &InputSpec{
		Vars:      []string{"z"},
		Eliminate: []string{"z"},
		Budget:    2,
		Constraints: []InputConstraint{
			{ID: "l1", Coeffs: map[string]string{"z": "-1"}, Op: "<=", RHS: "0"},
			{ID: "l2", Coeffs: map[string]string{"z": "-1"}, Op: "<=", RHS: "1"},
			{ID: "u1", Coeffs: map[string]string{"z": "1"}, Op: "<=", RHS: "3"},
			{ID: "u2", Coeffs: map[string]string{"z": "1"}, Op: "<=", RHS: "4"},
		},
	}
	rep, code, err := run(spec, 2, 100, false)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != "budget_exceeded" {
		t.Fatalf("expected budget_exceeded, got %s", rep.Status)
	}
	if code != exitBudget {
		t.Fatalf("expected exit code %d, got %d", exitBudget, code)
	}
	if len(rep.Constraints) != 0 {
		t.Fatal("budget-exhausted report must not contain constraints")
	}
}

func TestDuplicateAndDominatedEvidence(t *testing.T) {
	// c1: x <= 1 ; c2: 2x <= 2 (duplicate) ; c3: x <= 3 (dominated).
	ineqs := []*Ineq{
		ineqFrom([]string{"1"}, "1", false, "c1"),
		ineqFrom([]string{"2"}, "2", false, "c2"),
		ineqFrom([]string{"1"}, "3", false, "c3"),
	}
	res, err := eliminateAll(ineqs, nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Kept) != 1 {
		t.Fatalf("expected 1 kept, got %d", len(res.Kept))
	}
	if len(res.Kept[0].Prov) != 2 {
		t.Fatalf("kept constraint should carry 2 derivations, got %v", res.Kept[0].Prov)
	}
	if len(res.Removed) != 2 {
		t.Fatalf("expected 2 removed, got %d", len(res.Removed))
	}
	for _, r := range res.Removed {
		if len(r.Ineq.Prov) == 0 {
			t.Fatal("removed constraint lost its provenance")
		}
		if r.Reason == "" {
			t.Fatal("removed constraint missing reason")
		}
	}
}

func TestImplicitEqualityDetection(t *testing.T) {
	ineqs := []*Ineq{
		ineqFrom([]string{"1", "1"}, "3", false, "c1"),
		ineqFrom([]string{"-1", "-1"}, "-3", false, "c2"),
		ineqFrom([]string{"1", "0"}, "5", false, "c3"),
	}
	eqs := implicitEqualities(compact(ineqs, []int{0, 1}))
	if len(eqs) != 1 {
		t.Fatalf("expected 1 implicit equality, got %v", eqs)
	}
}

func TestVerificationCleanOnExample(t *testing.T) {
	// z <= x + y, z >= x - y, y <= 2 ; eliminate z -> y >= 0, y <= 2.
	spec := &InputSpec{
		Vars:      []string{"x", "y", "z"},
		Eliminate: []string{"z"},
		Constraints: []InputConstraint{
			{ID: "c1", Coeffs: map[string]string{"x": "1", "y": "1", "z": "-1"}, Op: ">=", RHS: "0"},
			{ID: "c2", Coeffs: map[string]string{"x": "1", "y": "-1", "z": "-1"}, Op: "<=", RHS: "0"},
			{ID: "c3", Coeffs: map[string]string{"y": "1"}, Op: "<=", RHS: "2"},
		},
	}
	rep, code, err := run(spec, 1000, 500, false)
	if err != nil {
		t.Fatal(err)
	}
	if code != exitOK || rep.Status != "ok" {
		t.Fatalf("unexpected status %s", rep.Status)
	}
	if !rep.Verification.FeasibilityAgree {
		t.Fatal("feasibility disagreement")
	}
	if len(rep.Verification.Mismatches) != 0 {
		t.Fatalf("sample mismatches: %v", rep.Verification.Mismatches)
	}
	if rep.Verification.SamplesChecked == 0 {
		t.Fatal("no samples checked")
	}
	if len(rep.Constraints) != 2 {
		t.Fatalf("expected 2 projected constraints, got %d", len(rep.Constraints))
	}
}

func TestInfeasibleReportAgreesWithSolver(t *testing.T) {
	spec := &InputSpec{
		Vars:      []string{"x", "y"},
		Eliminate: []string{"y"},
		Constraints: []InputConstraint{
			{ID: "c1", Coeffs: map[string]string{"x": "-1", "y": "1"}, Op: "<=", RHS: "0"},
			{ID: "c2", Coeffs: map[string]string{"x": "1", "y": "-1"}, Op: "<", RHS: "0"},
		},
	}
	rep, _, err := run(spec, 100, 100, false)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != "infeasible" {
		t.Fatalf("expected infeasible, got %s", rep.Status)
	}
	if rep.Contradiction == nil {
		t.Fatal("missing contradiction evidence")
	}
	if rep.Verification.OriginalFeasible {
		t.Fatal("independent solver should confirm infeasibility")
	}
	if !rep.Verification.FeasibilityAgree {
		t.Fatal("feasibility should agree on infeasible system")
	}
}

func TestRandomSystemsVerify(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 60; trial++ {
		nvars := 3
		ncon := 2 + rng.Intn(5)
		var ineqs []*Ineq
		for i := 0; i < ncon; i++ {
			coeffs := make([]*big.Rat, nvars)
			for j := range coeffs {
				coeffs[j] = big.NewRat(int64(rng.Intn(7)-3), 1)
			}
			rhs := big.NewRat(int64(rng.Intn(11)-5), 1)
			strict := rng.Intn(4) == 0
			ineqs = append(ineqs, newIneq(coeffs, rhs, strict, "r"+big.NewInt(int64(i)).String()))
		}
		res, err := eliminateAll(ineqs, []int{0}, 100000)
		if err != nil {
			t.Fatal(err)
		}
		remIdx := []int{1, 2}
		remNames := []string{"v1", "v2"}
		rep := verify(ineqs, nvars, res.Kept, remIdx, res.Contradiction != nil, 200, remNames)
		if !rep.FeasibilityAgree {
			t.Fatalf("trial %d: feasibility disagreement", trial)
		}
		if len(rep.Mismatches) != 0 {
			t.Fatalf("trial %d: mismatches %v", trial, rep.Mismatches)
		}
	}
}

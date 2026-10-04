package planner

import (
	"strings"
	"testing"
)

func compileOrFail(t *testing.T, spec *Spec) *Instance {
	t.Helper()
	inst, err := Compile(spec)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return inst
}

func baseSpec() *Spec {
	return &Spec{
		AsOf:        "2026-10-04",
		MaxEvidence: 25,
		Evidence: []Evidence{
			{ID: "E1", Cost: 120, ValidUntil: "2027-03-31"},
			{ID: "E2", Cost: 80, ValidUntil: "2027-01-15", MutexWith: []string{"E3"}},
			{ID: "E3", Cost: 60, ValidUntil: "2027-06-30"},
			{ID: "E4", Cost: 200, ValidUntil: "2026-09-30"}, // expired at as-of
			{ID: "E5", Cost: 45, ValidUntil: "2028-01-01"},
			{ID: "E6", Cost: 90, ValidFrom: "2026-11-01", ValidUntil: "2027-11-01"}, // not yet valid
			{ID: "E7", Cost: 70, ValidUntil: "2027-02-28", Group: "disk"},
			{ID: "E8", Cost: 65, ValidUntil: "2027-02-28", Group: "disk"},
		},
	}
}

func TestParsePrecedence(t *testing.T) {
	root, err := ParseExpr("E1 AND E2 OR E3")
	if err != nil {
		t.Fatalf("ParseExpr: %v", err)
	}
	if root.Kind != KindOr {
		t.Fatalf("AND must bind tighter than OR, got root kind %v", root.Kind)
	}
	root, err = ParseExpr("NOT E1 AND E2")
	if err != nil {
		t.Fatalf("ParseExpr: %v", err)
	}
	if root.Kind != KindAnd || root.Children[0].Kind != KindNot {
		t.Fatalf("NOT must bind tightest, got %v", root)
	}
}

func TestParseAliases(t *testing.T) {
	for _, src := range []string{"E1 && !E2", "E1 and not E2", "(E1) && !(E2)"} {
		root, err := ParseExpr(src)
		if err != nil {
			t.Fatalf("ParseExpr(%q): %v", src, err)
		}
		if root.Kind != KindAnd {
			t.Fatalf("ParseExpr(%q): expected AND root", src)
		}
	}
}

func TestEvaluateAndVsOr(t *testing.T) {
	spec := baseSpec()
	spec.Criteria = []Criterion{
		{ID: "ALL", Expr: "E1 AND E3"},
		{ID: "ANY", Expr: "E1 OR E3"},
	}
	inst := compileOrFail(t, spec)
	collected := map[string]bool{"E1": true} // only one of the two

	andTrace := Evaluate(inst.Criteria[0].Root, collected, inst)
	if andTrace.Satisfied {
		t.Fatalf("AND must NOT be satisfied when only one operand holds (任一≠全部)")
	}
	orTrace := Evaluate(inst.Criteria[1].Root, collected, inst)
	if !orTrace.Satisfied {
		t.Fatalf("OR must be satisfied when one operand holds")
	}
}

func TestSolveMinCostAndSharing(t *testing.T) {
	spec := baseSpec()
	spec.Criteria = []Criterion{
		{ID: "AC1", Expr: "(E1 AND E3) OR E4"}, // E4 expired -> E1+E3
		{ID: "AC2", Expr: "E2 OR E3"},          // E3 shared beats E2? E3=60 already in plan
		{ID: "AC3", Expr: "E5 AND (E7 OR E8) AND NOT E4"},
		{ID: "AC4", Expr: "E6 OR E3"}, // E6 not yet valid -> E3
	}
	inst := compileOrFail(t, spec)
	plan := Solve(inst)
	if !plan.Feasible {
		t.Fatalf("expected feasible plan")
	}
	// E1+E3 (180) + E5 (45) + E8 (65, cheaper than E7) = 290.
	want := 290.0
	if plan.TotalCost != want {
		t.Fatalf("total cost = %.2f, want %.2f (evidence %v)", plan.TotalCost, want, plan.Evidence)
	}
	got := map[string]bool{}
	for _, id := range plan.Evidence {
		got[id] = true
	}
	for _, wantID := range []string{"E1", "E3", "E5", "E8"} {
		if !got[wantID] {
			t.Fatalf("plan missing %s: %v", wantID, plan.Evidence)
		}
	}
	if got["E4"] || got["E6"] {
		t.Fatalf("expired/not-yet-valid evidence must never be collected: %v", plan.Evidence)
	}
	if len(plan.ExpiredSkip) == 0 {
		t.Fatalf("expired evidence should be reported as skipped")
	}
}

func TestSolveMutexConflictInfeasible(t *testing.T) {
	spec := baseSpec()
	spec.Criteria = []Criterion{
		{ID: "AC1", Expr: "E2 AND E3"}, // mutex pair
	}
	inst := compileOrFail(t, spec)
	plan := Solve(inst)
	if plan.Feasible {
		t.Fatalf("mutex-paired evidence must not appear together")
	}
	if !strings.Contains(plan.Criteria[0].Reason, "mutually exclusive") {
		t.Fatalf("reason should mention mutex, got: %s", plan.Criteria[0].Reason)
	}
}

func TestSolveExpiredOnlyPath(t *testing.T) {
	spec := baseSpec()
	spec.Criteria = []Criterion{{ID: "AC1", Expr: "E4"}}
	inst := compileOrFail(t, spec)
	plan := Solve(inst)
	if plan.Feasible {
		t.Fatalf("expired evidence must not satisfy a criterion")
	}
	if !strings.Contains(plan.Criteria[0].Reason, "expired") {
		t.Fatalf("reason should mention expiry, got: %s", plan.Criteria[0].Reason)
	}
}

func TestSolveContradiction(t *testing.T) {
	spec := baseSpec()
	spec.Criteria = []Criterion{{ID: "AC1", Expr: "E1 AND NOT E1"}}
	inst := compileOrFail(t, spec)
	plan := Solve(inst)
	if plan.Feasible {
		t.Fatalf("self-contradictory expression must be infeasible")
	}
	if !strings.Contains(plan.Criteria[0].Reason, "self-contradictory") {
		t.Fatalf("unexpected reason: %s", plan.Criteria[0].Reason)
	}
}

func TestSolveCrossCriterionConflict(t *testing.T) {
	spec := baseSpec()
	spec.Criteria = []Criterion{
		{ID: "AC1", Expr: "E2"},
		{ID: "AC2", Expr: "E3"}, // E2 and E3 are mutex
	}
	inst := compileOrFail(t, spec)
	plan := Solve(inst)
	if plan.Feasible {
		t.Fatalf("cross-criterion mutex conflict must be infeasible")
	}
	for _, cp := range plan.Criteria {
		if !strings.Contains(cp.Reason, "conflicts with the requirements of other criteria") {
			t.Fatalf("criterion %s individually satisfiable, reason should say so: %s", cp.CriterionID, cp.Reason)
		}
	}
}

func TestSolveNotConstraintRespected(t *testing.T) {
	spec := baseSpec()
	spec.Criteria = []Criterion{
		{ID: "AC1", Expr: "E1"},
		{ID: "AC2", Expr: "E3 AND NOT E1"},
	}
	inst := compileOrFail(t, spec)
	plan := Solve(inst)
	if plan.Feasible {
		t.Fatalf("NOT E1 conflicts with collecting E1")
	}
}

func TestSolveEvidenceLimit(t *testing.T) {
	spec := &Spec{AsOf: "2026-10-04", MaxEvidence: 2}
	for _, id := range []string{"A", "B", "C"} {
		spec.Evidence = append(spec.Evidence, Evidence{ID: id, Cost: 1})
		spec.Criteria = append(spec.Criteria, Criterion{ID: "AC-" + id, Expr: id})
	}
	inst := compileOrFail(t, spec)
	plan := Solve(inst)
	if plan.Feasible {
		t.Fatalf("3 required evidence items exceed the limit of 2")
	}
}

func TestSolveGroupMutex(t *testing.T) {
	spec := baseSpec()
	spec.Criteria = []Criterion{{ID: "AC1", Expr: "E7 AND E8"}} // same group
	inst := compileOrFail(t, spec)
	plan := Solve(inst)
	if plan.Feasible {
		t.Fatalf("same-group evidence must be mutually exclusive")
	}
}

func TestSolvePicksCheaperBranch(t *testing.T) {
	spec := baseSpec()
	spec.Criteria = []Criterion{{ID: "AC1", Expr: "E7 OR E8"}}
	inst := compileOrFail(t, spec)
	plan := Solve(inst)
	if !plan.Feasible || len(plan.Evidence) != 1 || plan.Evidence[0] != "E8" {
		t.Fatalf("expected cheapest branch E8, got %v", plan.Evidence)
	}
	if plan.Criteria[0].Term == nil || !plan.Criteria[0].Term.Require["E8"] {
		t.Fatalf("satisfaction path should record E8")
	}
}

func TestDeMorganTerms(t *testing.T) {
	root, err := ParseExpr("NOT (E1 OR E2)")
	if err != nil {
		t.Fatalf("ParseExpr: %v", err)
	}
	terms := Terms(root)
	if len(terms) != 1 || !terms[0].Forbid["E1"] || !terms[0].Forbid["E2"] {
		t.Fatalf("NOT (E1 OR E2) should forbid both, got %+v", terms)
	}
}

func TestDefaultLimitIs25(t *testing.T) {
	spec := &Spec{AsOf: "2026-10-04"}
	spec.Evidence = append(spec.Evidence, Evidence{ID: "A", Cost: 1})
	spec.Criteria = append(spec.Criteria, Criterion{ID: "AC1", Expr: "A"})
	inst := compileOrFail(t, spec)
	if inst.MaxEvidence != 25 {
		t.Fatalf("default evidence limit must be 25, got %d", inst.MaxEvidence)
	}
}

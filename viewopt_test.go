package viewopt

import (
	"fmt"
	"math"
	"testing"
)

func fullView(name string, cost int64, filter Interval) View {
	return View{Name: name, Dims: []int{0}, Filter: filter, ReadCost: cost}
}

func mustQuery(t *testing.T, q Query, tab Table) Result {
	t.Helper()
	r, err := EvaluateQuery(q, tab)
	if err != nil {
		t.Fatalf("EvaluateQuery: %v", err)
	}
	return r
}

func mustPlan(t *testing.T, q Query, views []View, p Plan, tab Table) Result {
	t.Helper()
	r, err := EvaluatePlan(q, views, p, tab)
	if err != nil {
		t.Fatalf("EvaluatePlan: %v", err)
	}
	return r
}

func row(key Key, f int64, m Value) Row {
	return Row{Key: key, Filter: f, Measure: m}
}

func TestNullGroupAndNullSum(t *testing.T) {
	q := Query{Dims: []int{0}, Filter: Between(0, 10)}
	tab := Table{
		row(K(Null()), 1, Null()),
		row(K(Int(0)), 1, Null()),
		row(K(Int(0)), 2, Int(5)),
	}
	got := mustQuery(t, q, tab)
	if len(got) != 2 {
		t.Fatalf("got %d groups, want 2 (NULL group is independent): %s", len(got), got)
	}
	nullGroup := got[K(Null())]
	if nullGroup.Count != 1 || nullGroup.CountNonNull != 0 || !nullGroup.Sum.Null {
		t.Fatalf("NULL group = %s, want count=1, count_nonnull=0, sum=NULL", nullGroup)
	}
	zeroGroup := got[K(Int(0))]
	if zeroGroup.Count != 2 || zeroGroup.CountNonNull != 1 || zeroGroup.Sum.Null || zeroGroup.Sum.V != 5 {
		t.Fatalf("group 0 = %s, want count=2, count_nonnull=1, sum=5", zeroGroup)
	}
	if _, ok := zeroGroup.Avg(); !ok {
		t.Fatal("AVG should be present for a non-empty non-null group")
	}
}

func TestEvaluatePlanEmptySumStaysNull(t *testing.T) {
	q := Query{Dims: []int{0}, Filter: Between(0, 10)}
	views := []View{fullView("all", 1, Between(0, 10))}
	p := Plan{Terms: []PlanTerm{{View: 0, Coeff: 1}}}
	tab := Table{row(K(Int(7)), 3, Null())}
	got := mustPlan(t, q, views, p, tab)
	a := got[K(Int(7))]
	if !a.Sum.Null {
		t.Fatalf("SUM over all-NULL measures = %s, want NULL (not 0)", a.Sum)
	}
	if a.Count != 1 || a.CountNonNull != 0 {
		t.Fatalf("aggregate = %s, want count=1, count_nonnull=0", a)
	}
}

func TestMissingViewGroupIsNotAZeroRow(t *testing.T) {
	q := Query{Dims: []int{0}, Filter: Between(0, 10)}
	views := []View{
		fullView("left", 1, Between(0, 5)),
		fullView("right", 1, Between(6, 10)),
	}
	p := Plan{Terms: []PlanTerm{{View: 0, Coeff: 1}, {View: 1, Coeff: 1}}}
	tab := Table{row(K(Int(0)), 1, Int(7))}
	got := mustPlan(t, q, views, p, tab)
	a, ok := got[K(Int(0))]
	if !ok {
		t.Fatal("group present in only one view must still appear")
	}
	if a.Count != 1 || a.CountNonNull != 1 || a.Sum.V != 7 {
		t.Fatalf("missing group in right view must contribute nothing, got %s", a)
	}
	if _, ok := got[K(Int(1))]; ok {
		t.Fatal("a group absent from every view must not be materialized as zero")
	}
}

func TestLinearViewsMatchDirectQuery(t *testing.T) {
	q := Query{Dims: []int{0}, Filter: Between(0, 10)}
	views := []View{
		fullView("left", 1, Between(0, 5)),
		fullView("right", 1, Between(6, 10)),
	}
	p := Plan{Terms: []PlanTerm{{View: 0, Coeff: 1}, {View: 1, Coeff: 1}}}
	tab := Table{
		row(K(Int(0)), 0, Int(2)),
		row(K(Int(0)), 5, Int(3)),
		row(K(Int(1)), 6, Int(7)),
		row(K(Int(1)), 10, Null()),
		row(K(Int(2)), 20, Int(99)), // outside query
	}
	want := mustQuery(t, q, tab)
	got := mustPlan(t, q, views, p, tab)
	if !want.Equal(got) {
		t.Fatalf("plan result %s != direct %s", got, want)
	}
}

func TestOverlappingPlanHasExtraEmptyGroup(t *testing.T) {
	q := Query{Dims: []int{0}, Filter: Between(5, 10)}
	views := []View{
		fullView("wide", 1, Between(0, 10)),
		fullView("prefix", 1, Between(0, 4)),
	}
	p := Plan{Terms: []PlanTerm{{View: 0, Coeff: 1}, {View: 1, Coeff: -1}}}
	tab := Table{
		row(K(Int(0)), 1, Int(100)), // only in the non-query prefix
		row(K(Int(1)), 6, Int(9)),   // in the query
	}
	direct := mustQuery(t, q, tab)
	plan := mustPlan(t, q, views, p, tab)

	// For key 1 the aggregates are identical.
	a, ok := direct[K(Int(1))]
	if !ok {
		t.Fatal("direct query lost key 1")
	}
	b, ok := plan[K(Int(1))]
	if !ok || !a.Equal(b) {
		t.Fatalf("key 1 aggregates differ: direct=%s plan=%s", a, b)
	}
	// But key 0 is an extra empty group in the plan.
	extra, ok := plan[K(Int(0))]
	if !ok {
		t.Fatalf("expected extra empty group for key 0: %s", plan)
	}
	if extra.Count != 0 || extra.CountNonNull != 0 || !extra.Sum.Null {
		t.Fatalf("extra group is not the empty aggregate: %s", extra)
	}
	if _, ok := direct[K(Int(0))]; ok {
		t.Fatal("direct query should not have key 0")
	}

	ver, err := VerifyPlan(q, views, p, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if ver.Equivalent {
		t.Fatal("plan with an extra empty group must not be accepted")
	}
	if ver.Counterexample == nil || len(ver.Counterexample.Table) != 1 {
		t.Fatalf("expected a one-row counterexample, got %+v", ver.Counterexample)
	}
}

func TestHavingCanFilterExtraEmptyGroup(t *testing.T) {
	q := Query{
		Dims:   []int{0},
		Filter: Between(5, 10),
		Having: func(a Agg) bool { return a.Count > 0 },
	}
	views := []View{
		fullView("wide", 1, Between(0, 10)),
		fullView("prefix", 1, Between(0, 4)),
	}
	p := Plan{Terms: []PlanTerm{{View: 0, Coeff: 1}, {View: 1, Coeff: -1}}}
	ver, err := VerifyPlan(q, views, p, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if !ver.Equivalent {
		t.Fatalf("HAVING COUNT(*)>0 filters the phantom empty group; got %s", ver.Reason)
	}
}

func TestAverageIsDerivedNotAdded(t *testing.T) {
	q := Query{Dims: []int{0}, Filter: Between(0, 10)}
	views := []View{
		fullView("a", 1, Between(0, 5)),
		fullView("b", 1, Between(6, 10)),
	}
	p := Plan{Terms: []PlanTerm{{View: 0, Coeff: 1}, {View: 1, Coeff: 1}}}
	tab := Table{
		row(K(Int(0)), 0, Int(10)),
		row(K(Int(0)), 1, Int(10)),
		row(K(Int(0)), 6, Int(20)),
	}
	got := mustPlan(t, q, views, p, tab)[K(Int(0))]
	avg, ok := got.Avg()
	if !ok {
		t.Fatal("AVG should be present")
	}
	if math.Abs(avg-40.0/3.0) > 1e-9 {
		t.Fatalf("AVG = %v, want %v (weighted by counts, not an average of view AVGs)", avg, 40.0/3.0)
	}
	if math.Abs(avg-15.0) < 1e-9 {
		t.Fatal("AVG was incorrectly formed by averaging view AVGs")
	}
}

func TestOptimizerPicksCheapestExactPlan(t *testing.T) {
	q := Query{Dims: []int{0}, Filter: Between(0, 10)}
	views := []View{
		fullView("expensive", 10, Between(0, 10)),
		fullView("cheap", 3, Between(0, 10)),
		fullView("medium", 4, Between(0, 10)),
	}
	sol, err := Optimize(Problem{Views: views, Query: q, Options: DefaultOptions()})
	if err != nil {
		t.Fatal(err)
	}
	if !sol.Equivalent {
		t.Fatalf("expected an equivalent plan: %s", sol.Reason)
	}
	if sol.Cost != 3 || len(sol.Plan.Terms) != 1 || sol.Plan.Terms[0].View != 1 || sol.Plan.Terms[0].Coeff != 1 {
		t.Fatalf("got plan %+v cost %d, want view 1 once at cost 3", sol.Plan, sol.Cost)
	}
}

func TestOptimizerUsesNegativeCorrection(t *testing.T) {
	q := Query{Dims: []int{0}, Filter: Between(0, 10)}
	views := []View{
		fullView("whole", 100, Between(0, 10)),
		fullView("left", 1, Between(0, 7)),
		fullView("right", 1, Between(5, 10)),
		fullView("overlap", 1, Between(5, 7)),
	}
	sol, err := Optimize(Problem{Views: views, Query: q, Options: DefaultOptions()})
	if err != nil {
		t.Fatal(err)
	}
	if !sol.Equivalent {
		t.Fatalf("expected plan: %s", sol.Reason)
	}
	if sol.Cost != 3 {
		t.Fatalf("cost = %d, want 3", sol.Cost)
	}
	want := Plan{Terms: []PlanTerm{
		{View: 1, Coeff: 1},
		{View: 2, Coeff: 1},
		{View: 3, Coeff: -1},
	}}
	if got := sol.Plan.Normalize(); !planEqual(got, want.Normalize()) {
		t.Fatalf("plan = %+v, want %+v", got, want)
	}
}

func planEqual(a, b Plan) bool {
	a, b = a.Normalize(), b.Normalize()
	if len(a.Terms) != len(b.Terms) {
		return false
	}
	for i := range a.Terms {
		if a.Terms[i] != b.Terms[i] {
			return false
		}
	}
	return true
}

func TestTenViewsMeetInTheMiddle(t *testing.T) {
	q := Query{Dims: []int{0}, Filter: Between(0, 99)}
	var views []View
	var terms []PlanTerm
	for i := 0; i < 10; i++ {
		lo, hi := int64(10*i), int64(10*i+9)
		views = append(views, fullView(fmt.Sprintf("v%d", i), 1, Between(lo, hi)))
		terms = append(terms, PlanTerm{View: i, Coeff: 1})
	}
	sol, err := Optimize(Problem{Views: views, Query: q, Options: DefaultOptions()})
	if err != nil {
		t.Fatal(err)
	}
	if !sol.Equivalent || sol.Cost != 10 {
		t.Fatalf("expected ten-view plan at cost 10, got equivalent=%v cost=%d reason=%s",
			sol.Equivalent, sol.Cost, sol.Reason)
	}
	if len(sol.Plan.Terms) != 10 {
		t.Fatalf("expected all ten terms, got %+v", sol.Plan)
	}
}

func TestTenViewsWithLargerCoefficientBound(t *testing.T) {
	q := Query{Dims: []int{0}, Filter: Between(0, 99)}
	var views []View
	for i := 0; i < 10; i++ {
		views = append(views, fullView(fmt.Sprintf("v%d", i), 1,
			Between(int64(10*i), int64(10*i+9))))
	}
	opt := DefaultOptions()
	opt.MaxAbsCoeff = 3
	sol, err := Optimize(Problem{Views: views, Query: q, Options: opt})
	if err != nil {
		t.Fatal(err)
	}
	if !sol.Equivalent || sol.Cost != 10 {
		t.Fatalf("expected ten-view plan at cost 10, got %+v", sol)
	}
}

func TestSparseTwoToTheFortyEndpoints(t *testing.T) {
	base := int64(1) << 40
	q := Query{Dims: []int{0}, Filter: Between(base-100, base)}
	views := []View{
		fullView("left", 1, Between(base-100, base-51)),
		fullView("right", 1, Between(base-50, base)),
	}
	sol, err := Optimize(Problem{Views: views, Query: q, Options: DefaultOptions()})
	if err != nil {
		t.Fatal(err)
	}
	if !sol.Equivalent || sol.Cost != 2 {
		t.Fatalf("expected two-view plan at cost 2, got %+v", sol)
	}
	tab := Table{
		row(K(Int(0)), base-100, Int(1)),
		row(K(Int(0)), base-50, Int(2)),
		row(K(Int(0)), base, Int(3)),
	}
	if !mustQuery(t, q, tab).Equal(mustPlan(t, q, views, sol.Plan, tab)) {
		t.Fatal("sparse-endpoint plan disagrees with direct query")
	}
}

func TestThreeDimensionsAndNullGroups(t *testing.T) {
	q := Query{Dims: []int{0, 1, 2}, Filter: Between(0, 10)}
	views := []View{{
		Name: "fine", Dims: []int{0, 1, 2}, Filter: Between(0, 10), ReadCost: 1,
	}}
	p := Plan{Terms: []PlanTerm{{View: 0, Coeff: 1}}}
	tab := Table{
		row(K(Int(0), Int(1), Null()), 1, Int(2)),
		row(K(Int(0), Int(1), Int(0)), 1, Int(3)),
		row(K(Int(0), Int(1), Null()), 1, Null()),
	}
	if !mustQuery(t, q, tab).Equal(mustPlan(t, q, views, p, tab)) {
		t.Fatal("three-dimensional plan disagrees with direct query")
	}
}

func TestReorderedAndExtraViewDimensions(t *testing.T) {
	q := Query{Dims: []int{1, 0}, Filter: Between(0, 10)}
	views := []View{
		{Name: "reordered", Dims: []int{0, 1}, Filter: Between(0, 5), ReadCost: 1},
		{Name: "extra", Dims: []int{0, 1, 2}, Filter: Between(6, 10), ReadCost: 1},
	}
	p := Plan{Terms: []PlanTerm{{View: 0, Coeff: 1}, {View: 1, Coeff: 1}}}
	tab := Table{
		row(K(Int(4), Int(3), Int(9)), 1, Int(2)),
		row(K(Int(4), Int(3), Int(8)), 1, Int(5)),
		row(K(Int(5), Int(2), Int(7)), 6, Int(11)),
	}
	if !mustQuery(t, q, tab).Equal(mustPlan(t, q, views, p, tab)) {
		t.Fatal("reordered/extra view dimensions disagree with direct query")
	}
	ver, err := VerifyPlan(q, views, p, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if !ver.Equivalent {
		t.Fatalf("plan should be equivalent: %s", ver.Reason)
	}
}

func TestNoGroupingDimensions(t *testing.T) {
	q := Query{Dims: nil, Filter: Between(0, 10)}
	views := []View{{Name: "global", Dims: nil, Filter: Between(0, 10), ReadCost: 1}}
	p := Plan{Terms: []PlanTerm{{View: 0, Coeff: 1}}}
	tab := Table{
		row(K(Int(1), Int(2), Int(3)), 1, Int(4)),
		row(K(Int(5), Int(6), Int(7)), 2, Null()),
	}
	direct := mustQuery(t, q, tab)
	plan := mustPlan(t, q, views, p, tab)
	if !direct.Equal(plan) {
		t.Fatalf("global aggregate mismatch: direct=%s plan=%s", direct, plan)
	}
	a := direct[K()]
	if a.Count != 2 || a.CountNonNull != 1 || a.Sum.V != 4 {
		t.Fatalf("global aggregate = %s", a)
	}
}

func TestOptimizerFailureReturnsMinimalCounterexample(t *testing.T) {
	q := Query{Dims: []int{0}, Filter: Between(0, 10)}
	sol, err := Optimize(Problem{Views: nil, Query: q, Options: DefaultOptions()})
	if err != nil {
		t.Fatal(err)
	}
	if sol.Equivalent {
		t.Fatal("no views cannot implement a non-empty query")
	}
	if sol.Counterexample == nil || len(sol.Counterexample.Table) != 1 {
		t.Fatalf("expected one-row counterexample, got %+v", sol.Counterexample)
	}
}

func TestOptimizerAcceptsPlanWhenHavingFiltersEmpty(t *testing.T) {
	q := Query{
		Dims:   []int{0},
		Filter: Between(5, 10),
		Having: func(a Agg) bool { return a.Count > 0 },
	}
	views := []View{
		fullView("wide", 5, Between(0, 10)),
		fullView("prefix", 1, Between(0, 4)),
		fullView("exact", 10, Between(5, 10)),
	}
	sol, err := Optimize(Problem{Views: views, Query: q, Options: DefaultOptions()})
	if err != nil {
		t.Fatal(err)
	}
	if !sol.Equivalent {
		t.Fatalf("expected wide-prefix plan to be accepted: %s", sol.Reason)
	}
	if sol.Cost != 6 {
		t.Fatalf("cost = %d, want 6", sol.Cost)
	}
}

func TestCoarseViewOutsideQueryStillLeaks(t *testing.T) {
	// Interval coefficients cancel outside the query, but the coarse view
	// aggregates the two fine keys together.  HAVING COUNT(*)>0 discards a
	// truly empty group, yet the coarse row is non-empty and must be rejected.
	q := Query{
		Dims:   []int{0, 1},
		Filter: Between(5, 10),
		Having: func(a Agg) bool { return a.Count > 0 },
	}
	views := []View{
		{Name: "fine-query", Dims: []int{0, 1}, Filter: Between(5, 10), ReadCost: 1},
		{Name: "coarse-prefix", Dims: []int{0}, Filter: Between(0, 4), ReadCost: 1},
		{Name: "fine-prefix", Dims: []int{0, 1}, Filter: Between(0, 4), ReadCost: 1},
	}
	p := Plan{Terms: []PlanTerm{
		{View: 0, Coeff: 1},
		{View: 1, Coeff: 1},
		{View: 2, Coeff: -1},
	}}
	ver, err := VerifyPlan(q, views, p, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if ver.Equivalent {
		t.Fatal("coarse view can leave an extra non-empty group outside the query")
	}
	if ver.Counterexample == nil || len(ver.Counterexample.Table) != 1 {
		t.Fatalf("expected a one-row counterexample, got %+v", ver.Counterexample)
	}
	if ver.Counterexample.Table[0].Filter > 4 {
		t.Fatalf("counterexample should exercise the coarse prefix, got %v", ver.Counterexample.Table[0])
	}
}

func TestStructuralMismatchHasCounterexample(t *testing.T) {
	q := Query{Dims: []int{0, 1}, Filter: Between(0, 10)}
	views := []View{{
		Name: "coarse", Dims: []int{0}, Filter: Between(0, 10), ReadCost: 1,
	}}
	p := Plan{Terms: []PlanTerm{{View: 0, Coeff: 1}}}
	ver, err := VerifyPlan(q, views, p, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if ver.Equivalent {
		t.Fatal("view missing query dimension 1 must be rejected")
	}
	if ver.Counterexample == nil || len(ver.Counterexample.Table) != 1 {
		t.Fatalf("expected one-row counterexample, got %+v", ver.Counterexample)
	}
	r := ver.Counterexample.Table[0]
	if r.Key[1].Null {
		t.Fatalf("counterexample should make the missing dimension non-NULL: %v", r)
	}
}

func TestMinimalCounterexampleTupleOrder(t *testing.T) {
	q := Query{Dims: []int{0}, Filter: Between(0, 10)}
	ver, err := VerifyPlan(q, nil, Plan{}, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if ver.Equivalent {
		t.Fatal("empty plan cannot cover a non-empty query")
	}
	if ver.Counterexample == nil || len(ver.Counterexample.Table) != 1 {
		t.Fatalf("expected one-row counterexample, got %+v", ver.Counterexample)
	}
	r := ver.Counterexample.Table[0]
	if r.Key != (Key{Null(), Null(), Null()}) || r.Filter != 0 || !r.Measure.Null {
		t.Fatalf("counterexample row = %v, want smallest tuple (NULL,NULL,NULL)@0=NULL", r)
	}
}

func TestPlanCostModels(t *testing.T) {
	views := []View{{Name: "v", Dims: []int{0}, Filter: Between(0, 1), ReadCost: 3}}
	cost, err := (Plan{Terms: []PlanTerm{{View: 0, Coeff: -2}}}).Cost(views)
	if err != nil {
		t.Fatal(err)
	}
	if cost != 3 {
		t.Fatalf("default cost = %d, want 3 (one read)", cost)
	}
	cost, err = (Plan{Terms: []PlanTerm{{View: 0, Coeff: -2}}}).CostWeighted(views)
	if err != nil {
		t.Fatal(err)
	}
	if cost != 6 {
		t.Fatalf("weighted cost = %d, want 6", cost)
	}
}

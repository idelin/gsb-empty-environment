package decorrelator

import (
	"strings"
	"testing"
)

func mustUnnest(t *testing.T, sql string) PlanNode {
	t.Helper()
	stmt, err := Parse(sql)
	if err != nil {
		t.Fatalf("parse %q: %v", sql, err)
	}
	plan, err := Unnest(stmt)
	if err != nil {
		t.Fatalf("unnest %q: %v", sql, err)
	}
	return plan
}

func mustSemiJoin(t *testing.T, sql string) *HashSemiJoin {
	t.Helper()
	plan := mustUnnest(t, sql)
	proj, ok := plan.(*Project)
	if !ok {
		t.Fatalf("top node = %T, want *Project", plan)
	}
	semi, ok := proj.Input.(*HashSemiJoin)
	if !ok {
		t.Fatalf("project input = %T, want *HashSemiJoin\nplan:\n%s", proj.Input, Explain(plan))
	}
	return semi
}

// Acceptance criterion: the correlated EXISTS is decorrelated into a
// Hash Semi-Join on d.id = e.dept_id.
func TestExistsDecorrelatesToHashSemiJoin(t *testing.T) {
	sql := "SELECT * FROM depts d WHERE EXISTS (SELECT 1 FROM emps e WHERE e.dept_id = d.id)"
	semi := mustSemiJoin(t, sql)

	if len(semi.LeftKeys) != 1 || len(semi.RightKeys) != 1 {
		t.Fatalf("want exactly one join key pair, got %d/%d", len(semi.LeftKeys), len(semi.RightKeys))
	}
	if got := ExprString(semi.LeftKeys[0]); got != "d.id" {
		t.Errorf("left key = %q, want %q", got, "d.id")
	}
	if got := ExprString(semi.RightKeys[0]); got != "e.dept_id" {
		t.Errorf("right key = %q, want %q", got, "e.dept_id")
	}

	scanL, ok := semi.Left.(*Scan)
	if !ok || scanL.Table != "depts" || scanL.Alias != "d" {
		t.Errorf("left input = %v, want Scan(depts d)", describe(semi.Left))
	}
	scanR, ok := semi.Right.(*Scan)
	if !ok || scanR.Table != "emps" || scanR.Alias != "e" {
		t.Errorf("right input = %v, want Scan(emps e)", describe(semi.Right))
	}

	plan := mustUnnest(t, sql)
	explain := Explain(plan)
	if !strings.Contains(explain, "HashSemiJoin(d.id = e.dept_id)") {
		t.Errorf("explain missing hash semi-join condition:\n%s", explain)
	}
	if sql := RenderSQL(plan); !strings.Contains(sql, "SEMI JOIN emps e ON d.id = e.dept_id") {
		t.Errorf("rendered SQL missing semi join: %s", sql)
	}
}

// Inner-only predicates of the subquery are pushed down as a filter on the
// inner side of the semi join.
func TestExistsWithInnerFilter(t *testing.T) {
	semi := mustSemiJoin(t,
		"SELECT * FROM depts d WHERE EXISTS (SELECT 1 FROM emps e WHERE e.dept_id = d.id AND e.active = 1)")
	filter, ok := semi.Right.(*Filter)
	if !ok {
		t.Fatalf("right input = %T, want *Filter", semi.Right)
	}
	if got := ExprString(filter.Pred); got != "e.active = 1" {
		t.Errorf("inner filter = %q, want %q", got, "e.active = 1")
	}
}

// Outer-only predicates stay as a Filter above the semi join.
func TestOuterResidualPredicateStaysOnTop(t *testing.T) {
	plan := mustUnnest(t,
		"SELECT * FROM depts d WHERE d.budget > 1000 AND EXISTS (SELECT 1 FROM emps e WHERE e.dept_id = d.id)")
	proj := plan.(*Project)
	filter, ok := proj.Input.(*Filter)
	if !ok {
		t.Fatalf("project input = %T, want *Filter\nplan:\n%s", proj.Input, Explain(plan))
	}
	if got := ExprString(filter.Pred); got != "d.budget > 1000" {
		t.Errorf("residual filter = %q, want %q", got, "d.budget > 1000")
	}
	if _, ok := filter.Input.(*HashSemiJoin); !ok {
		t.Fatalf("filter input = %T, want *HashSemiJoin", filter.Input)
	}
}

// Correlated IN becomes a semi join; correlation predicates add join keys.
func TestCorrelatedInBecomesSemiJoin(t *testing.T) {
	semi := mustSemiJoin(t,
		"SELECT * FROM emps e WHERE e.dept_id IN (SELECT d.id FROM depts d WHERE d.region = e.region)")
	if len(semi.LeftKeys) != 2 {
		t.Fatalf("want 2 join key pairs, got %d", len(semi.LeftKeys))
	}
	got := joinCond(semi.LeftKeys, semi.RightKeys, nil)
	want := "e.dept_id = d.id AND e.region = d.region"
	if got != want {
		t.Errorf("join condition = %q, want %q", got, want)
	}
}

// Uncorrelated IN becomes an inner join against a deduplicated subquery.
func TestUncorrelatedInBecomesInnerJoinWithDistinct(t *testing.T) {
	plan := mustUnnest(t,
		"SELECT * FROM emps e WHERE e.dept_id IN (SELECT d.id FROM depts d WHERE d.region = 'EU')")
	proj := plan.(*Project)
	join, ok := proj.Input.(*HashInnerJoin)
	if !ok {
		t.Fatalf("project input = %T, want *HashInnerJoin\nplan:\n%s", proj.Input, Explain(plan))
	}
	if got := joinCond(join.LeftKeys, join.RightKeys, nil); got != "e.dept_id = d.id" {
		t.Errorf("join condition = %q, want %q", got, "e.dept_id = d.id")
	}
	distinct, ok := join.Right.(*Distinct)
	if !ok {
		t.Fatalf("right input = %T, want *Distinct", join.Right)
	}
	filter, ok := distinct.Input.(*Filter)
	if !ok {
		t.Fatalf("distinct input = %T, want *Filter", distinct.Input)
	}
	if got := ExprString(filter.Pred); got != "d.region = 'EU'" {
		t.Errorf("inner filter = %q, want %q", got, "d.region = 'EU'")
	}
}

// Multiple EXISTS conjuncts chain into multiple semi joins.
func TestMultipleExistsChain(t *testing.T) {
	plan := mustUnnest(t,
		"SELECT * FROM depts d WHERE EXISTS (SELECT 1 FROM emps e WHERE e.dept_id = d.id)"+
			" AND EXISTS (SELECT 1 FROM budgets b WHERE b.dept_id = d.id)")
	proj := plan.(*Project)
	outer, ok := proj.Input.(*HashSemiJoin)
	if !ok {
		t.Fatalf("project input = %T, want *HashSemiJoin", proj.Input)
	}
	if got := ExprString(outer.RightKeys[0]); got != "b.dept_id" {
		t.Errorf("outer semi-join right key = %q, want %q", got, "b.dept_id")
	}
	inner, ok := outer.Left.(*HashSemiJoin)
	if !ok {
		t.Fatalf("outer semi-join left = %T, want *HashSemiJoin", outer.Left)
	}
	if got := ExprString(inner.RightKeys[0]); got != "e.dept_id" {
		t.Errorf("inner semi-join right key = %q, want %q", got, "e.dept_id")
	}
}

// Non-equality correlation predicates become residual join conditions.
func TestNonEqualityCorrelationBecomesResidual(t *testing.T) {
	semi := mustSemiJoin(t,
		"SELECT * FROM depts d WHERE EXISTS (SELECT 1 FROM emps e WHERE e.dept_id = d.id AND e.salary > d.min_salary)")
	if len(semi.LeftKeys) != 1 {
		t.Fatalf("want 1 equality key, got %d", len(semi.LeftKeys))
	}
	if len(semi.Residual) != 1 || ExprString(semi.Residual[0]) != "e.salary > d.min_salary" {
		t.Errorf("residual = %v, want [e.salary > d.min_salary]", semi.Residual)
	}
}

// Unqualified columns inside the subquery are treated as inner-side.
func TestUnqualifiedColumnIsInner(t *testing.T) {
	semi := mustSemiJoin(t,
		"SELECT * FROM depts d WHERE EXISTS (SELECT 1 FROM emps e WHERE dept_id = d.id)")
	if got := ExprString(semi.RightKeys[0]); got != "dept_id" {
		t.Errorf("right key = %q, want %q", got, "dept_id")
	}
}

func TestErrors(t *testing.T) {
	cases := []struct {
		name string
		sql  string
	}{
		{"IN subquery with two columns", "SELECT * FROM emps e WHERE e.dept_id IN (SELECT d.id, d.name FROM depts d)"},
		{"IN subquery with star", "SELECT * FROM emps e WHERE e.dept_id IN (SELECT * FROM depts d)"},
		{"unknown table qualifier", "SELECT * FROM depts d WHERE EXISTS (SELECT 1 FROM emps e WHERE x.dept_id = d.id)"},
		{"outer-only subquery predicate", "SELECT * FROM depts d WHERE EXISTS (SELECT 1 FROM emps e WHERE d.id > 5)"},
		{"unknown IN left operand table", "SELECT * FROM emps e WHERE z.dept_id IN (SELECT d.id FROM depts d)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stmt, err := Parse(tc.sql)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if _, err := Unnest(stmt); err == nil {
				t.Errorf("expected error for %q, got none", tc.sql)
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	for _, sql := range []string{
		"SELECT FROM depts",
		"SELECT * depts",
		"SELECT * FROM depts WHERE EXISTS SELECT 1 FROM emps",
		"SELECT * FROM depts WHERE d.id =",
		"SELECT * FROM depts WHERE",
	} {
		if _, err := Parse(sql); err == nil {
			t.Errorf("expected parse error for %q, got none", sql)
		}
	}
}

func TestParseBasics(t *testing.T) {
	stmt, err := Parse("select d.id, d.name from depts as d where d.id = 1 and d.name = 'it''s' or d.id < 2;")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(stmt.Columns) != 2 || stmt.Columns[0].Table != "d" || stmt.Columns[0].Name != "id" {
		t.Errorf("columns = %+v", stmt.Columns)
	}
	if len(stmt.From) != 1 || stmt.From[0].Name != "depts" || stmt.From[0].Alias != "d" {
		t.Errorf("from = %+v", stmt.From)
	}
	or, ok := stmt.Where.(*BinaryExpr)
	if !ok || or.Op != "OR" {
		t.Fatalf("where = %T, want OR at top", stmt.Where)
	}
}
